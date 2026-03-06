package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/session"
)

// SessionsListTool lists all session keys known to the current agent's SessionManager.
type SessionsListTool struct {
	sessions *session.SessionManager
}

func NewSessionsListTool(sessions *session.SessionManager) *SessionsListTool {
	return &SessionsListTool{sessions: sessions}
}

func (t *SessionsListTool) Name() string { return "sessions_list" }

func (t *SessionsListTool) Description() string {
	return "List available session keys for the current agent."
}

func (t *SessionsListTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *SessionsListTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.sessions == nil {
		return ErrorResult("sessions manager not configured")
	}
	keys := t.sessions.ListKeys()
	if len(keys) == 0 {
		return NewToolResult("No sessions.")
	}
	b, _ := json.Marshal(keys)
	return NewToolResult(string(b))
}

// SessionsHistoryTool returns message history for a session.
type SessionsHistoryTool struct {
	sessions *session.SessionManager
}

func NewSessionsHistoryTool(sessions *session.SessionManager) *SessionsHistoryTool {
	return &SessionsHistoryTool{sessions: sessions}
}

func (t *SessionsHistoryTool) Name() string { return "sessions_history" }

func (t *SessionsHistoryTool) Description() string {
	return "Get message history for a session key."
}

func (t *SessionsHistoryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_key": map[string]any{
				"type":        "string",
				"description": "Target session key",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Optional max number of messages from the end (0 = all)",
			},
		},
		"required": []string{"session_key"},
	}
}

func (t *SessionsHistoryTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.sessions == nil {
		return ErrorResult("sessions manager not configured")
	}
	key, _ := args["session_key"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		return ErrorResult("session_key is required")
	}

	limit := 0
	if v, ok := args["limit"].(float64); ok { // JSON numbers decode as float64
		limit = int(v)
	}
	if v, ok := args["limit"].(int); ok {
		limit = v
	}
	if limit < 0 {
		limit = 0
	}

	history := t.sessions.GetHistory(key)
	if limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	b, err := json.Marshal(history)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to encode history: %v", err))
	}
	return NewToolResult(string(b))
}

// SessionsSendTool appends a message to a session.
type SessionsSendTool struct {
	sessions *session.SessionManager
}

func NewSessionsSendTool(sessions *session.SessionManager) *SessionsSendTool {
	return &SessionsSendTool{sessions: sessions}
}

func (t *SessionsSendTool) Name() string { return "sessions_send" }

func (t *SessionsSendTool) Description() string {
	return "Append a message to a session (does not automatically run the agent)."
}

func (t *SessionsSendTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_key": map[string]any{
				"type":        "string",
				"description": "Target session key",
			},
			"role": map[string]any{
				"type":        "string",
				"description": "Message role (user|assistant|system)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Message content",
			},
			"save": map[string]any{
				"type":        "boolean",
				"description": "Whether to persist session to disk (default true)",
			},
		},
		"required": []string{"session_key", "content"},
	}
}

func (t *SessionsSendTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.sessions == nil {
		return ErrorResult("sessions manager not configured")
	}
	key, _ := args["session_key"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		return ErrorResult("session_key is required")
	}
	content, _ := args["content"].(string)
	if strings.TrimSpace(content) == "" {
		return ErrorResult("content is required")
	}
	role, _ := args["role"].(string)
	role = strings.TrimSpace(role)
	if role == "" {
		role = "user"
	}

	save := true
	if v, ok := args["save"].(bool); ok {
		save = v
	}

	t.sessions.AddMessage(key, role, content)
	if save {
		_ = t.sessions.Save(key)
	}
	return NewToolResult("ok")
}
