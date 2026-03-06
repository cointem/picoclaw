package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/routing"
	"github.com/sipeed/picoclaw/pkg/session"
)

type SessionsSpawnTarget struct {
	AgentID        string
	Provider       providers.LLMProvider
	Model          string
	Workspace      string
	Sessions       *session.SessionManager
	Tools          *ToolRegistry
	MaxIterations  int
	LLMOptions     map[string]any
	Bus            *bus.MessageBus
	AllowDeliver   bool
	OriginChannel  string
	OriginChatID   string
	Announce       bool
	AnnounceSender string
}

// SessionsSpawnTool creates a new subagent session and runs it asynchronously.
// It persists the full message flow into the target SessionManager.
type SessionsSpawnTool struct {
	defaultTarget  SessionsSpawnTarget
	allowlistCheck func(targetAgentID string) bool
	resolveTarget  func(targetAgentID string) (SessionsSpawnTarget, bool)
}

var _ AsyncExecutor = (*SessionsSpawnTool)(nil)

func NewSessionsSpawnTool(defaultTarget SessionsSpawnTarget) *SessionsSpawnTool {
	return &SessionsSpawnTool{defaultTarget: defaultTarget}
}

func (t *SessionsSpawnTool) Name() string { return "sessions_spawn" }

func (t *SessionsSpawnTool) Description() string {
	return "Spawn a subagent as a new session key (agent:<id>:subagent:<uuid>) and run it asynchronously."
}

func (t *SessionsSpawnTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The task for the subagent session",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Optional short label",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Optional target agent ID",
			},
			"deliver": map[string]any{
				"type":        "boolean",
				"description": "Whether subagent can use message tool to talk to the user (default false)",
			},
			"announce": map[string]any{
				"type":        "boolean",
				"description": "Whether to announce completion back to the caller (default true)",
			},
			"inherit_last": map[string]any{
				"type":        "integer",
				"description": "How many recent parent messages to include as context (default 8)",
				"minimum":     0.0,
				"maximum":     50.0,
			},
			"parent_session_key": map[string]any{
				"type":        "string",
				"description": "Optional parent session key to inherit context from (default: current session)",
			},
		},
		"required": []string{"task"},
	}
}

func (t *SessionsSpawnTool) SetAllowlistChecker(check func(targetAgentID string) bool) {
	t.allowlistCheck = check
}

func (t *SessionsSpawnTool) SetTargetResolver(resolver func(targetAgentID string) (SessionsSpawnTarget, bool)) {
	t.resolveTarget = resolver
}

func (t *SessionsSpawnTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, nil)
}

func (t *SessionsSpawnTool) ExecuteAsync(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	return t.execute(ctx, args, cb)
}

func (t *SessionsSpawnTool) execute(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	task, _ := args["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return ErrorResult("task is required and must be a non-empty string")
	}

	label, _ := args["label"].(string)
	agentID, _ := args["agent_id"].(string)
	agentID = strings.TrimSpace(agentID)

	deliver := false
	if v, ok := args["deliver"].(bool); ok {
		deliver = v
	}
	announce := true
	if v, ok := args["announce"].(bool); ok {
		announce = v
	}

	inheritLast := 8
	if v, ok := args["inherit_last"].(float64); ok {
		inheritLast = int(v)
	}
	if v, ok := args["inherit_last"].(int); ok {
		inheritLast = v
	}
	if inheritLast < 0 {
		inheritLast = 0
	}
	if inheritLast > 50 {
		inheritLast = 50
	}

	parentSessionKey, _ := args["parent_session_key"].(string)
	parentSessionKey = strings.TrimSpace(parentSessionKey)
	if parentSessionKey == "" {
		parentSessionKey = strings.TrimSpace(ToolSessionKey(ctx))
	}

	// Check allowlist if targeting a specific agent
	if agentID != "" && t.allowlistCheck != nil {
		if !t.allowlistCheck(agentID) {
			return ErrorResult(fmt.Sprintf("not allowed to spawn agent '%s'", agentID))
		}
	}

	originChannel := ToolChannel(ctx)
	if originChannel == "" {
		originChannel = "cli"
	}
	originChatID := ToolChatID(ctx)
	if originChatID == "" {
		originChatID = "direct"
	}

	target := t.defaultTarget
	target.OriginChannel = originChannel
	target.OriginChatID = originChatID
	target.AllowDeliver = deliver
	target.Announce = announce
	if target.AnnounceSender == "" {
		target.AnnounceSender = "sessions_spawn"
	}

	if agentID != "" {
		if t.resolveTarget == nil {
			return ErrorResult("target agent resolver not configured")
		}
		resolved, ok := t.resolveTarget(agentID)
		if !ok {
			return ErrorResult(fmt.Sprintf("target agent '%s' is not available for sessions_spawn", agentID))
		}
		resolved.OriginChannel = originChannel
		resolved.OriginChatID = originChatID
		resolved.AllowDeliver = deliver
		resolved.Announce = announce
		if resolved.AnnounceSender == "" {
			resolved.AnnounceSender = "sessions_spawn"
		}
		target = resolved
	}

	if target.Provider == nil {
		return ErrorResult("LLM provider not configured")
	}
	if target.Sessions == nil {
		return ErrorResult("sessions manager not configured")
	}
	if strings.TrimSpace(target.Model) == "" {
		return ErrorResult("model not configured")
	}

	subID := uuid.NewString()
	agentNorm := routing.NormalizeAgentID(target.AgentID)
	sessionKey := fmt.Sprintf("agent:%s:subagent:%s", agentNorm, subID)

	systemPrompt := `You are a subagent session. Complete the given task independently.
Use tools as needed. When done, provide a clear summary.
Respond in the same language as the user unless explicitly asked otherwise.`

	// Persist initial context
	target.Sessions.AddMessage(sessionKey, "system", systemPrompt)

	// Optionally inherit recent parent context as an extra system message.
	if inheritLast > 0 && parentSessionKey != "" && parentSessionKey != sessionKey {
		parent := target.Sessions.GetHistory(parentSessionKey)
		if len(parent) > 0 {
			start := 0
			if len(parent) > inheritLast {
				start = len(parent) - inheritLast
			}
			var b strings.Builder
			b.WriteString("Parent context (recent messages):\n")
			for _, m := range parent[start:] {
				role := strings.TrimSpace(m.Role)
				if role == "" || role == "tool" {
					continue
				}
				content := strings.TrimSpace(m.Content)
				if content == "" {
					continue
				}
				if len(content) > 800 {
					content = content[:800]
				}
				b.WriteString(fmt.Sprintf("- %s: %s\n", role, content))
			}
			ctxMsg := strings.TrimSpace(b.String())
			if ctxMsg != "" {
				target.Sessions.AddMessage(sessionKey, "system", ctxMsg)
			}
		}
	}

	target.Sessions.AddMessage(sessionKey, "user", task)
	_ = target.Sessions.Save(sessionKey)

	// Tools: optionally strip message tool if deliver is false.
	runTools := target.Tools
	if runTools != nil {
		// Disallow recursive spawning from subagent sessions.
		runTools = runTools.CloneExcept("spawn", "sessions_spawn")
		if !deliver {
			runTools = runTools.CloneExcept("message")
		}
	}

	messages := target.Sessions.GetHistory(sessionKey)

	go func() {
		runCtx := context.Background()
		loopCfg := ToolLoopConfig{
			Provider:      target.Provider,
			Model:         target.Model,
			Tools:         runTools,
			MaxIterations: target.MaxIterations,
			LLMOptions:    target.LLMOptions,
			OnMessage: func(msg providers.Message) {
				target.Sessions.AddFullMessage(sessionKey, msg)
			},
		}

		channelForTools := originChannel
		chatForTools := originChatID
		if !deliver {
			// still keep origin in ctx for non-message tools; message tool is removed.
			channelForTools = originChannel
			chatForTools = originChatID
		}

		result, err := RunToolLoop(runCtx, loopCfg, messages, channelForTools, chatForTools)
		finalText := ""
		if err != nil {
			finalText = fmt.Sprintf("Error: %v", err)
			target.Sessions.AddMessage(sessionKey, "assistant", finalText)
		} else {
			finalText = result.Content
		}
		_ = target.Sessions.Save(sessionKey)

		tr := &ToolResult{
			ForLLM:  finalText,
			ForUser: finalText,
			Silent:  false,
			IsError: err != nil,
			Async:   false,
			Err:     err,
		}

		if cb != nil {
			cb(ctx, tr)
		}

		if target.Announce && target.Bus != nil {
			name := strings.TrimSpace(label)
			if name == "" {
				name = sessionKey
			}
			announceContent := fmt.Sprintf("Task '%s' completed.\n\nSession: %s\n\nResult:\n%s", name, sessionKey, finalText)
			pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			target.Bus.PublishInbound(pubCtx, bus.InboundMessage{
				Channel:  "system",
				SenderID: fmt.Sprintf("%s:%s", target.AnnounceSender, subID),
				ChatID:   fmt.Sprintf("%s:%s", originChannel, originChatID),
				Content:  announceContent,
			})
		}
	}()

	return AsyncResult(fmt.Sprintf("accepted: session=%s", sessionKey))
}
