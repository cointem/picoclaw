package memoryplugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools"
)

func TestMemoryMem0Plugin_LoadAndToolsAndAppendix(t *testing.T) {
	ctx := context.Background()

	var gotAuth string
	var gotSearchBody string
	var gotAddBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/v2/memories/search" {
			b, _ := io.ReadAll(r.Body)
			gotSearchBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"results":[{"id":"m1","memory":"Allergic to nuts","user_id":"sess","categories":["health"],"created_at":"2025-01-01T00:00:00Z","score":0.42}]}`))
			return
		}
		if r.URL.Path == "/v1/memories/" {
			b, _ := io.ReadAll(r.Body)
			gotAddBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`[{"id":"mem_1","event":"ADD","data":{"memory":"User is allergic to nuts"}}]`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	reg := tools.NewToolRegistry()
	mgr := NewManager(Options{Workspace: t.TempDir(), AgentID: "main", Tools: reg})

	pluginCfg, _ := json.Marshal(map[string]any{
		"api_key":        "test-key",
		"base_url":       srv.URL,
		"auto_recall":    true,
		"recall_top_k":   3,
		"user_id_mode":   "session_key",
		"recall_max_chars": 50,
	})

	err := mgr.LoadFromConfig(ctx, config.MemoryConfig{
		Enabled: true,
		Plugins: []config.MemoryPluginSpec{{
			ID:     "memory-mem0",
			Config: pluginCfg,
		}},
	})
	if err != nil {
		t.Fatalf("LoadFromConfig: %v", err)
	}

	for _, name := range []string{"memory_search", "memory_store", "memory_get", "memory_forget"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("expected %s tool registered", name)
		}
	}

	appendix := mgr.BuildSystemAppendix(ctx, SystemAppendixInput{SessionKey: "sess", UserMessage: "dietary restrictions"})
	if appendix == "" {
		t.Fatalf("expected non-empty appendix")
	}
	if !strings.Contains(appendix, "Allergic") {
		t.Fatalf("expected appendix to contain returned memory; got: %s", appendix)
	}
	if gotAuth != "Token test-key" {
		t.Fatalf("expected Authorization header, got %q", gotAuth)
	}
	if !strings.Contains(gotSearchBody, `"query":"dietary restrictions"`) {
		t.Fatalf("expected search body to include query, got: %s", gotSearchBody)
	}
	if !strings.Contains(gotSearchBody, `"user_id":"sess"`) {
		t.Fatalf("expected search body to include user_id filter, got: %s", gotSearchBody)
	}

	res := reg.Execute(ctx, "memory_store", map[string]any{"content": "I am allergic to nuts", "user_id": "sess"})
	if res == nil || res.IsError || res.Err != nil {
		t.Fatalf("memory_store error: %+v", res)
	}
	if !strings.Contains(gotAddBody, `"user_id":"sess"`) {
		t.Fatalf("expected add body to include user_id, got: %s", gotAddBody)
	}
	if !strings.Contains(gotAddBody, `"messages"`) {
		t.Fatalf("expected add body to include messages, got: %s", gotAddBody)
	}
}
