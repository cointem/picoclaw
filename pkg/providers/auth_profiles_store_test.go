package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthProfilesStore_PersistCooldown(t *testing.T) {
	ws := t.TempDir()
	store, err := NewAuthProfilesStore(ws)
	if err != nil {
		t.Fatalf("NewAuthProfilesStore() error = %v", err)
	}

	ns := store.NamespaceKey("openai", "https://api.openai.com/v1")
	profile := "key-1"

	// Failure should put profile into cooldown.
	store.MarkFailure(ns, profile, FailoverRateLimit)
	if store.IsAvailable(ns, profile) {
		t.Fatalf("expected profile to be unavailable after failure")
	}
	if rem := store.CooldownRemaining(ns, profile); rem <= 0 {
		t.Fatalf("CooldownRemaining() = %v, want > 0", rem)
	}

	// Reload from disk and ensure state is kept.
	store2, err := NewAuthProfilesStore(ws)
	if err != nil {
		t.Fatalf("NewAuthProfilesStore(reload) error = %v", err)
	}
	if store2.IsAvailable(ns, profile) {
		t.Fatalf("expected profile to remain unavailable after reload")
	}

	// Ensure file exists in the expected location.
	stateFile := filepath.Join(ws, "state", "auth_profiles.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("expected state file to exist: %v", err)
	}
}
