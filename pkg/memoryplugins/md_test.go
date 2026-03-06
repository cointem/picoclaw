package memoryplugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools"
)

func TestMemoryMDPlugin_LoadAndAppendix(t *testing.T) {
	ctx := context.Background()
	ws := t.TempDir()

	memDir := filepath.Join(ws, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("hello world memory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewToolRegistry()
	mgr := NewManager(Options{Workspace: ws, AgentID: "main", Tools: reg})

	pluginCfg, _ := json.Marshal(map[string]any{
		"auto_recall": true,
		"recall_days": 1,
	})

	err := mgr.LoadFromConfig(ctx, config.MemoryConfig{
		Enabled: true,
		Plugins: []config.MemoryPluginSpec{{
			ID:     "memory-md",
			Config: pluginCfg,
		}},
	})
	if err != nil {
		t.Fatalf("LoadFromConfig: %v", err)
	}

	if _, ok := reg.Get("memory_search"); !ok {
		t.Fatalf("expected memory_search tool registered")
	}
	if _, ok := reg.Get("memory_get"); !ok {
		t.Fatalf("expected memory_get tool registered")
	}
	if _, ok := reg.Get("memory_store"); !ok {
		t.Fatalf("expected memory_store tool registered")
	}

	appendix := mgr.BuildSystemAppendix(ctx, SystemAppendixInput{UserMessage: "hello"})
	if appendix == "" {
		t.Fatalf("expected non-empty appendix")
	}
	if !strings.Contains(appendix, "hello") {
		t.Fatalf("expected appendix to contain query snippet; got: %s", appendix)
	}
}
