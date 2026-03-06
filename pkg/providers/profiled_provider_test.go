package providers

import (
	"context"
	"fmt"
	"testing"
)

type stubProvider struct {
	name      string
	calls     int
	returnErr error
}

func (s *stubProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	s.calls++
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return &LLMResponse{Content: "ok:" + s.name}, nil
}

func (s *stubProvider) GetDefaultModel() string { return "" }

func TestProfiledProvider_RotateAndSticky(t *testing.T) {
	ws := t.TempDir()
	store, err := NewAuthProfilesStore(ws)
	if err != nil {
		t.Fatalf("NewAuthProfilesStore() error = %v", err)
	}

	p1 := &stubProvider{name: "p1", returnErr: fmt.Errorf("status: 429 too many requests")}
	p2 := &stubProvider{name: "p2"}

	pp, err := NewProfiledProvider("openai", "https://api.openai.com/v1", store, []ProfileClient{
		{Name: "key-1", Provider: p1},
		{Name: "key-2", Provider: p2},
	})
	if err != nil {
		t.Fatalf("NewProfiledProvider() error = %v", err)
	}

	sessionKey := "cli:sticky"
	opts := map[string]any{"session_key": sessionKey}

	resp, err := pp.Chat(context.Background(), nil, nil, "gpt-4o", opts)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp == nil || resp.Content != "ok:p2" {
		t.Fatalf("resp = %#v, want ok:p2", resp)
	}
	if p1.calls != 1 {
		t.Fatalf("p1.calls = %d, want 1", p1.calls)
	}
	if p2.calls != 1 {
		t.Fatalf("p2.calls = %d, want 1", p2.calls)
	}

	// Second call should stick to p2 and not call p1 again.
	resp2, err := pp.Chat(context.Background(), nil, nil, "gpt-4o", opts)
	if err != nil {
		t.Fatalf("Chat(2) error = %v", err)
	}
	if resp2 == nil || resp2.Content != "ok:p2" {
		t.Fatalf("resp2 = %#v, want ok:p2", resp2)
	}
	if p1.calls != 1 {
		t.Fatalf("p1.calls(after) = %d, want 1", p1.calls)
	}
	if p2.calls != 2 {
		t.Fatalf("p2.calls(after) = %d, want 2", p2.calls)
	}
}
