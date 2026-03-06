package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/session"
)

func TestSessionsTools_ListHistorySend(t *testing.T) {
	sm := session.NewSessionManager(t.TempDir())
	listTool := NewSessionsListTool(sm)
	histTool := NewSessionsHistoryTool(sm)
	sendTool := NewSessionsSendTool(sm)

	key := "agent:main:main"
	if res := sendTool.Execute(context.Background(), map[string]any{"session_key": key, "content": "hello"}); res == nil || res.IsError {
		t.Fatalf("sessions_send failed: %+v", res)
	}

	res := listTool.Execute(context.Background(), nil)
	if res == nil || res.IsError {
		t.Fatalf("sessions_list failed: %+v", res)
	}
	if !strings.Contains(res.ForLLM, key) {
		t.Fatalf("expected sessions_list to contain %q, got %s", key, res.ForLLM)
	}

	h := histTool.Execute(context.Background(), map[string]any{"session_key": key})
	if h == nil || h.IsError {
		t.Fatalf("sessions_history failed: %+v", h)
	}
	if !strings.Contains(h.ForLLM, "hello") {
		t.Fatalf("expected history to contain message content, got %s", h.ForLLM)
	}
}

func TestSessionsSpawn_PersistsSession(t *testing.T) {
	provider := &MockLLMProvider{}
	sm := session.NewSessionManager(t.TempDir())

	spawn := NewSessionsSpawnTool(SessionsSpawnTarget{
		AgentID:       "main",
		Provider:      provider,
		Model:         "test-model",
		Sessions:      sm,
		Tools:         NewToolRegistry(),
		MaxIterations: 3,
		Announce:      false,
	})

	ctx := WithToolContext(context.Background(), "cli", "direct")
	res := spawn.Execute(ctx, map[string]any{"task": "Do something"})
	if res == nil || res.IsError || !res.Async {
		t.Fatalf("expected async accepted result, got: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "accepted: session=") {
		t.Fatalf("unexpected spawn response: %s", res.ForLLM)
	}
	parts := strings.Split(res.ForLLM, "accepted: session=")
	sessionKey := strings.TrimSpace(parts[len(parts)-1])
	if !strings.HasPrefix(sessionKey, "agent:main:subagent:") {
		t.Fatalf("unexpected session key: %q", sessionKey)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		h := sm.GetHistory(sessionKey)
		if len(h) >= 3 {
			// system + user + assistant
			if h[0].Role != "system" || h[1].Role != "user" {
				t.Fatalf("unexpected initial roles: %#v", h)
			}
			if !strings.Contains(h[len(h)-1].Content, "Task completed") {
				t.Fatalf("expected assistant content, got: %q", h[len(h)-1].Content)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for session to be populated; history=%d", len(h))
		}
		time.Sleep(20 * time.Millisecond)
	}
}
