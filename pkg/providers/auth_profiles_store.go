package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/fileutil"
)

// AuthProfilesStore persists per-provider auth-profile cooldown/disable state.
//
// This is the OpenClaw-inspired first-stage failover: rotate credentials (API keys)
// before falling back to another model.
//
// State is stored under: <workspace>/state/auth_profiles.json
//
// Thread-safe.
type AuthProfilesStore struct {
	mu            sync.RWMutex
	workspace     string
	stateFile     string
	failureWindow time.Duration
	nowFunc       func() time.Time
	data          authProfilesFile
}

type authProfilesFile struct {
	Version    int                               `json:"version"`
	Namespaces map[string]*authProfilesNamespace `json:"namespaces"`
}

type authProfilesNamespace struct {
	Profiles map[string]*authProfileState `json:"profiles"`
}

type authProfileState struct {
	ErrorCount     int                    `json:"error_count"`
	FailureCounts  map[FailoverReason]int `json:"failure_counts"`
	CooldownEnd    time.Time              `json:"cooldown_end,omitempty"`
	DisabledUntil  time.Time              `json:"disabled_until,omitempty"`
	DisabledReason FailoverReason         `json:"disabled_reason,omitempty"`
	LastFailure    time.Time              `json:"last_failure,omitempty"`
	LastUsed       time.Time              `json:"last_used,omitempty"`
}

func NewAuthProfilesStore(workspace string) (*AuthProfilesStore, error) {
	if workspace == "" {
		return nil, fmt.Errorf("auth profiles store: workspace is empty")
	}

	stateDir := filepath.Join(workspace, "state")
	stateFile := filepath.Join(stateDir, "auth_profiles.json")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("auth profiles store: mkdir state dir: %w", err)
	}

	s := &AuthProfilesStore{
		workspace:     workspace,
		stateFile:     stateFile,
		failureWindow: defaultFailureWindow,
		nowFunc:       time.Now,
		data: authProfilesFile{
			Version:    1,
			Namespaces: map[string]*authProfilesNamespace{},
		},
	}

	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *AuthProfilesStore) NamespaceKey(protocol, apiBase string) string {
	// apiBase distinguishes multiple endpoints under same protocol.
	return fmt.Sprintf("%s|%s", NormalizeProvider(protocol), apiBase)
}

func (s *AuthProfilesStore) IsAvailable(namespace, profile string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st := s.getProfileLocked(namespace, profile)
	if st == nil {
		return true
	}

	now := s.nowFunc()
	if !st.DisabledUntil.IsZero() && now.Before(st.DisabledUntil) {
		return false
	}
	if !st.CooldownEnd.IsZero() && now.Before(st.CooldownEnd) {
		return false
	}
	return true
}

func (s *AuthProfilesStore) CooldownRemaining(namespace, profile string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st := s.getProfileLocked(namespace, profile)
	if st == nil {
		return 0
	}

	now := s.nowFunc()
	var remaining time.Duration
	if !st.DisabledUntil.IsZero() && now.Before(st.DisabledUntil) {
		remaining = maxDuration(remaining, st.DisabledUntil.Sub(now))
	}
	if !st.CooldownEnd.IsZero() && now.Before(st.CooldownEnd) {
		remaining = maxDuration(remaining, st.CooldownEnd.Sub(now))
	}
	return remaining
}

func (s *AuthProfilesStore) GetLastUsed(namespace, profile string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st := s.getProfileLocked(namespace, profile)
	if st == nil {
		return time.Time{}
	}
	return st.LastUsed
}

func (s *AuthProfilesStore) MarkUsed(namespace, profile string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.getOrCreateProfileLocked(namespace, profile)
	st.LastUsed = s.nowFunc()
	_ = s.saveLocked() // best-effort
}

func (s *AuthProfilesStore) MarkFailure(namespace, profile string, reason FailoverReason) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowFunc()
	st := s.getOrCreateProfileLocked(namespace, profile)

	// 24h failure window reset.
	if !st.LastFailure.IsZero() && now.Sub(st.LastFailure) > s.failureWindow {
		st.ErrorCount = 0
		st.FailureCounts = make(map[FailoverReason]int)
	}

	st.ErrorCount++
	if st.FailureCounts == nil {
		st.FailureCounts = make(map[FailoverReason]int)
	}
	st.FailureCounts[reason]++
	st.LastFailure = now

	if reason == FailoverBilling {
		billingCount := st.FailureCounts[FailoverBilling]
		st.DisabledUntil = now.Add(calculateBillingCooldown(billingCount))
		st.DisabledReason = FailoverBilling
	} else {
		st.CooldownEnd = now.Add(calculateStandardCooldown(st.ErrorCount))
	}

	_ = s.saveLocked() // best-effort
}

func (s *AuthProfilesStore) MarkSuccess(namespace, profile string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.getProfileLocked(namespace, profile)
	if st == nil {
		return
	}

	st.ErrorCount = 0
	st.FailureCounts = make(map[FailoverReason]int)
	st.CooldownEnd = time.Time{}
	st.DisabledUntil = time.Time{}
	st.DisabledReason = ""
	st.LastFailure = time.Time{}

	_ = s.saveLocked() // best-effort
}

func (s *AuthProfilesStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("auth profiles store: read: %w", err)
	}

	var f authProfilesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("auth profiles store: unmarshal: %w", err)
	}

	if f.Namespaces == nil {
		f.Namespaces = map[string]*authProfilesNamespace{}
	}
	if f.Version == 0 {
		f.Version = 1
	}

	s.data = f
	return nil
}

func (s *AuthProfilesStore) saveLocked() error {
	// Must be called with write lock held.
	b, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("auth profiles store: marshal: %w", err)
	}
	return fileutil.WriteFileAtomic(s.stateFile, b, 0o600)
}

func (s *AuthProfilesStore) getProfileLocked(namespace, profile string) *authProfileState {
	ns := s.data.Namespaces[namespace]
	if ns == nil {
		return nil
	}
	return ns.Profiles[profile]
}

func (s *AuthProfilesStore) getOrCreateProfileLocked(namespace, profile string) *authProfileState {
	ns := s.data.Namespaces[namespace]
	if ns == nil {
		ns = &authProfilesNamespace{Profiles: map[string]*authProfileState{}}
		s.data.Namespaces[namespace] = ns
	}
	st := ns.Profiles[profile]
	if st == nil {
		st = &authProfileState{FailureCounts: map[FailoverReason]int{}}
		ns.Profiles[profile] = st
	}
	if st.FailureCounts == nil {
		st.FailureCounts = map[FailoverReason]int{}
	}
	return st
}

func maxDuration(a, b time.Duration) time.Duration {
	if b > a {
		return b
	}
	return a
}
