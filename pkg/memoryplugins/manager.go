package memoryplugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// Plugin is a memory extension that can register tools and optionally
// contribute dynamic context to the system prompt.
//
// Plugins are configured via config.json: {"memory": {"enabled": true, "plugins": [...]}}.
// This mirrors openclaw's pattern (manifest + register(api) + hooks), but implemented
// as in-process Go plugins.
//
// Register MUST be idempotent per plugin instance.
// Close is optional.
type Plugin interface {
	ID() string
	Register(ctx context.Context, api *API) error
}

// SystemAppendixHook is an optional hook to add dynamic system context
// (e.g. "relevant memories") for the current user message.
type SystemAppendixHook interface {
	Plugin
	BuildSystemAppendix(ctx context.Context, in SystemAppendixInput) (string, error)
}

// CloseHook is an optional cleanup hook.
type CloseHook interface {
	Plugin
	Close() error
}

type API struct {
	Workspace   string
	AgentID     string
	Tools       *tools.ToolRegistry
	PluginID    string
	PluginConfig json.RawMessage
}

type SystemAppendixInput struct {
	SessionKey  string
	Channel     string
	ChatID      string
	UserMessage string
}

type Constructor func(spec config.MemoryPluginSpec, opts Options) (Plugin, error)

type Options struct {
	Workspace string
	AgentID   string
	Tools     *tools.ToolRegistry
}

type Manager struct {
	workspace string
	agentID   string
	tools     *tools.ToolRegistry

	mu      sync.RWMutex
	plugins []Plugin
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Constructor{}
)

func Register(id string, ctor Constructor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[id] = ctor
}

func NewManager(opts Options) *Manager {
	return &Manager{
		workspace: opts.Workspace,
		agentID:   opts.AgentID,
		tools:     opts.Tools,
		plugins:   nil,
	}
}

func (m *Manager) LoadFromConfig(ctx context.Context, cfg config.MemoryConfig) error {
	if !cfg.Enabled {
		return nil
	}

	for _, spec := range cfg.Plugins {
		enabled := true
		if spec.Enabled != nil {
			enabled = *spec.Enabled
		}
		if !enabled {
			continue
		}

		plugin, err := m.instantiate(spec)
		if err != nil {
			return err
		}
		if plugin == nil {
			continue
		}

		api := &API{
			Workspace:    m.workspace,
			AgentID:      m.agentID,
			Tools:        m.tools,
			PluginID:     spec.ID,
			PluginConfig: spec.Config,
		}
		if err := plugin.Register(ctx, api); err != nil {
			return fmt.Errorf("memory plugin %q register: %w", spec.ID, err)
		}

		m.mu.Lock()
		m.plugins = append(m.plugins, plugin)
		m.mu.Unlock()

		logger.InfoCF("memory", "Memory plugin loaded", map[string]any{"agent_id": m.agentID, "plugin": spec.ID})
	}

	return nil
}

func (m *Manager) instantiate(spec config.MemoryPluginSpec) (Plugin, error) {
	registryMu.RLock()
	ctor, ok := registry[spec.ID]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown memory plugin id: %s", spec.ID)
	}
	return ctor(spec, Options{Workspace: m.workspace, AgentID: m.agentID, Tools: m.tools})
}

func (m *Manager) BuildSystemAppendix(ctx context.Context, in SystemAppendixInput) string {
	m.mu.RLock()
	plugins := append([]Plugin(nil), m.plugins...)
	m.mu.RUnlock()

	var out string
	for _, p := range plugins {
		h, ok := p.(SystemAppendixHook)
		if !ok {
			continue
		}
		part, err := h.BuildSystemAppendix(ctx, in)
		if err != nil {
			logger.WarnCF("memory", "Memory plugin BuildSystemAppendix failed", map[string]any{"plugin": p.ID(), "error": err.Error()})
			continue
		}
		if part == "" {
			continue
		}
		if out == "" {
			out = part
		} else {
			out += "\n\n---\n\n" + part
		}
	}
	return out
}

func (m *Manager) Close() error {
	m.mu.Lock()
	plugins := m.plugins
	m.plugins = nil
	m.mu.Unlock()

	var firstErr error
	for _, p := range plugins {
		if c, ok := p.(CloseHook); ok {
			if err := c.Close(); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("memory plugin %q close: %w", p.ID(), err)
			}
		}
	}
	return firstErr
}
