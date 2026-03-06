package providers

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type ProfileClient struct {
	Name     string
	Provider LLMProvider
}

// ProfiledProvider rotates across multiple auth profiles (e.g., API keys)
// within the same protocol/apiBase before the outer model fallback kicks in.
//
// Selection rules:
//   - Prefer sticky profile for the given session key (best-effort)
//   - Otherwise pick least-recently-used available profile
//   - On retriable errors: mark per-profile cooldown/disable and try next profile
//   - On non-retriable errors: abort immediately
//
// Cooldown/disable state is persisted via AuthProfilesStore.
//
// Note: This wrapper is intentionally protocol-scoped; it does not switch protocol.
type ProfiledProvider struct {
	protocol  string
	apiBase   string
	namespace string
	store     *AuthProfilesStore
	profiles  []ProfileClient

	stickyMu sync.RWMutex
	sticky   map[string]stickyEntry // sessionKey -> profile
}

type stickyEntry struct {
	Profile string
	Expiry  time.Time
}

const defaultStickyTTL = 6 * time.Hour

func NewProfiledProvider(protocol, apiBase string, store *AuthProfilesStore, profiles []ProfileClient) (*ProfiledProvider, error) {
	if store == nil {
		return nil, fmt.Errorf("profiled provider: store is nil")
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("profiled provider: no profiles")
	}
	for i := range profiles {
		if profiles[i].Provider == nil {
			return nil, fmt.Errorf("profiled provider: nil provider for profile %q", profiles[i].Name)
		}
		if profiles[i].Name == "" {
			profiles[i].Name = fmt.Sprintf("profile-%d", i+1)
		}
	}

	p := &ProfiledProvider{
		protocol:  NormalizeProvider(protocol),
		apiBase:   apiBase,
		namespace: store.NamespaceKey(protocol, apiBase),
		store:     store,
		profiles:  append([]ProfileClient(nil), profiles...),
		sticky:    map[string]stickyEntry{},
	}

	return p, nil
}

func (p *ProfiledProvider) GetDefaultModel() string { return "" }

func (p *ProfiledProvider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	sessionKey := ""
	if options != nil {
		if v, ok := options["session_key"].(string); ok {
			sessionKey = v
		}
	}

	order := p.buildAttemptOrder(sessionKey)

	var lastErr *FailoverError
	attempted := 0

	for _, idx := range order {
		if idx < 0 || idx >= len(p.profiles) {
			continue
		}
		prof := p.profiles[idx]

		if ctx.Err() == context.Canceled {
			return nil, context.Canceled
		}

		if !p.store.IsAvailable(p.namespace, prof.Name) {
			continue
		}

		attempted++
		p.store.MarkUsed(p.namespace, prof.Name)

		resp, err := prof.Provider.Chat(ctx, messages, tools, model, options)
		if err == nil {
			p.store.MarkSuccess(p.namespace, prof.Name)
			p.setSticky(sessionKey, prof.Name)
			return resp, nil
		}

		if ctx.Err() == context.Canceled {
			return nil, context.Canceled
		}

		failErr := ClassifyError(err, p.protocol, model)
		if failErr == nil {
			// Unknown errors: treat as retriable for profile rotation, but still record.
			failErr = &FailoverError{
				Reason:   FailoverUnknown,
				Provider: p.protocol,
				Model:    model,
				Wrapped:  err,
			}
		}

		lastErr = failErr

		if !failErr.IsRetriable() {
			return nil, failErr
		}

		p.store.MarkFailure(p.namespace, prof.Name, failErr.Reason)
	}

	// If we never attempted because all profiles are in cooldown/disabled,
	// return a classifiable retriable error so outer model fallback can proceed.
	if attempted == 0 {
		return nil, &FailoverError{
			Reason:   FailoverRateLimit,
			Provider: p.protocol,
			Model:    model,
			Wrapped:  fmt.Errorf("all auth profiles are in cooldown or disabled"),
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}

	return nil, fmt.Errorf("profiled provider: all attempts failed")
}

func (p *ProfiledProvider) buildAttemptOrder(sessionKey string) []int {
	order := make([]int, 0, len(p.profiles))
	used := make(map[int]bool, len(p.profiles))

	if sessionKey != "" {
		if sticky := p.getSticky(sessionKey); sticky != "" {
			if idx := p.indexOfProfile(sticky); idx >= 0 {
				order = append(order, idx)
				used[idx] = true
			}
		}
	}

	rest := make([]int, 0, len(p.profiles))
	for i := range p.profiles {
		if used[i] {
			continue
		}
		rest = append(rest, i)
	}

	sort.Slice(rest, func(i, j int) bool {
		a := rest[i]
		b := rest[j]
		la := p.store.GetLastUsed(p.namespace, p.profiles[a].Name)
		lb := p.store.GetLastUsed(p.namespace, p.profiles[b].Name)
		// Zero time sorts first (never used).
		if la.Equal(lb) {
			return p.profiles[a].Name < p.profiles[b].Name
		}
		if la.IsZero() {
			return true
		}
		if lb.IsZero() {
			return false
		}
		return la.Before(lb)
	})

	order = append(order, rest...)
	return order
}

func (p *ProfiledProvider) indexOfProfile(name string) int {
	for i := range p.profiles {
		if p.profiles[i].Name == name {
			return i
		}
	}
	return -1
}

func (p *ProfiledProvider) getSticky(sessionKey string) string {
	p.stickyMu.RLock()
	defer p.stickyMu.RUnlock()

	ent, ok := p.sticky[sessionKey]
	if !ok {
		return ""
	}
	if !ent.Expiry.IsZero() && time.Now().After(ent.Expiry) {
		return ""
	}
	return ent.Profile
}

func (p *ProfiledProvider) setSticky(sessionKey, profile string) {
	if sessionKey == "" || profile == "" {
		return
	}
	p.stickyMu.Lock()
	defer p.stickyMu.Unlock()
	p.sticky[sessionKey] = stickyEntry{Profile: profile, Expiry: time.Now().Add(defaultStickyTTL)}
}
