package memoryplugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/tools"
)

type mem0MemorySearchTool struct{ p *mem0Plugin }
type mem0MemoryStoreTool struct{ p *mem0Plugin }
type mem0MemoryGetTool struct{ p *mem0Plugin }
type mem0MemoryForgetTool struct{ p *mem0Plugin }

func newMem0MemorySearchTool(p *mem0Plugin) tools.Tool { return &mem0MemorySearchTool{p: p} }
func newMem0MemoryStoreTool(p *mem0Plugin) tools.Tool  { return &mem0MemoryStoreTool{p: p} }
func newMem0MemoryGetTool(p *mem0Plugin) tools.Tool    { return &mem0MemoryGetTool{p: p} }
func newMem0MemoryForgetTool(p *mem0Plugin) tools.Tool { return &mem0MemoryForgetTool{p: p} }

func (t *mem0MemorySearchTool) Name() string { return "memory_search" }

func (t *mem0MemorySearchTool) Description() string {
	return "在 Mem0 记忆中按语义检索相关内容（建议提供 user_id 进行隔离）。"
}

func (t *mem0MemorySearchTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "检索问题/短语",
			},
			"user_id": map[string]any{
				"type":        "string",
				"description": "可选：用户标识；不填则使用当前会话的 session_key",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "返回最多多少条结果（默认 10）",
			},
			"threshold": map[string]any{
				"type":        "number",
				"description": "最小相似度阈值（默认 0.3）",
			},
			"filters": map[string]any{
				"type":        "object",
				"description": "可选：Mem0 filters 对象（将自动补充 user_id）。",
			},
		},
		"required": []string{"query"},
	}
}

func (t *mem0MemorySearchTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return tools.ErrorResult("missing query")
	}

	userID, _ := args["user_id"].(string)
	userID = strings.TrimSpace(userID)

	filters, ok := args["filters"].(map[string]any)
	if !ok || filters == nil {
		filters = map[string]any{}
	}
	if userID != "" {
		filters["user_id"] = userID
	}
	if _, exists := filters["user_id"]; !exists {
		return tools.ErrorResult("missing user_id (pass user_id or filters.user_id)")
	}

	topK := intFromAny(args["top_k"], 10)
	thresholdAny, hasThreshold := args["threshold"]
	threshold := 0.3
	if hasThreshold {
		threshold = floatFromAny(thresholdAny, 0.3)
	}

	memories, err := t.p.client.Search(ctx, mem0SearchRequest{
		Query:     query,
		Filters:   filters,
		Version:   t.p.cfg.Version,
		TopK:      topK,
		Threshold: &threshold,
	})
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}

	payload := map[string]any{"matches": []map[string]any{}}
	out := make([]map[string]any, 0, len(memories))
	for _, m := range memories {
		row := map[string]any{
			"id":         m.ID,
			"memory":     m.Memory,
			"user_id":    m.UserID,
			"categories": m.Categories,
			"created_at": m.CreatedAt,
			"updated_at": m.UpdatedAt,
		}
		if m.Score != nil {
			row["score"] = *m.Score
		}
		out = append(out, row)
	}
	payload["matches"] = out
	b, _ := json.MarshalIndent(payload, "", "  ")
	return tools.SilentResult(string(b))
}

func (t *mem0MemoryStoreTool) Name() string { return "memory_store" }

func (t *mem0MemoryStoreTool) Description() string {
	return "将一条可长期记住的信息写入 Mem0（建议提供 user_id 进行隔离）。"
}

func (t *mem0MemoryStoreTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "要保存的内容",
			},
			"user_id": map[string]any{
				"type":        "string",
				"description": "可选：用户标识；不填则不写 user_id（不推荐）",
			},
			"metadata": map[string]any{
				"type":        "object",
				"description": "可选：附加元数据（键值对）",
			},
			"infer": map[string]any{
				"type":        "boolean",
				"description": "可选：是否让 Mem0 推断记忆（默认 true）",
			},
			"async_mode": map[string]any{
				"type":        "boolean",
				"description": "可选：是否异步处理（默认 true）",
			},
		},
		"required": []string{"content"},
	}
}

func (t *mem0MemoryStoreTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	content, _ := args["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return tools.ErrorResult("missing content")
	}

	userID, _ := args["user_id"].(string)
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return tools.ErrorResult("missing user_id")
	}

	metadata, _ := args["metadata"].(map[string]any)

	var inferPtr *bool
	if v, ok := args["infer"].(bool); ok {
		inferPtr = &v
	}
	var asyncPtr *bool
	if v, ok := args["async_mode"].(bool); ok {
		asyncPtr = &v
	}

	req := mem0AddRequest{
		UserID: userID,
		Messages: []mem0Message{{
			Role:    "user",
			Content: content,
		}},
		Metadata:     metadata,
		Infer:        inferPtr,
		AsyncMode:    asyncPtr,
		OutputFormat: "v1.1",
		Version:      t.p.cfg.Version,
	}

	events, err := t.p.client.Add(ctx, req)
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}
	b, _ := json.MarshalIndent(map[string]any{"events": events}, "", "  ")
	return tools.SilentResult(string(b))
}

func (t *mem0MemoryGetTool) Name() string { return "memory_get" }

func (t *mem0MemoryGetTool) Description() string {
	return "按 memory_id 从 Mem0 读取一条记忆（用于检查或调试）。"
}

func (t *mem0MemoryGetTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"memory_id": map[string]any{
				"type":        "string",
				"description": "Mem0 memory_id",
			},
		},
		"required": []string{"memory_id"},
	}
}

func (t *mem0MemoryGetTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	memoryID, _ := args["memory_id"].(string)
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return tools.ErrorResult("missing memory_id")
	}

	m, err := t.p.client.Get(ctx, memoryID)
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return tools.SilentResult(string(b))
}

func (t *mem0MemoryForgetTool) Name() string { return "memory_forget" }

func (t *mem0MemoryForgetTool) Description() string {
	return "按 memory_id 从 Mem0 删除一条记忆。"
}

func (t *mem0MemoryForgetTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"memory_id": map[string]any{
				"type":        "string",
				"description": "Mem0 memory_id",
			},
		},
		"required": []string{"memory_id"},
	}
}

func (t *mem0MemoryForgetTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	memoryID, _ := args["memory_id"].(string)
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return tools.ErrorResult("missing memory_id")
	}
	if err := t.p.client.Delete(ctx, memoryID); err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}
	return tools.SilentResult(fmt.Sprintf("deleted %s", memoryID))
}

func floatFromAny(v any, def float64) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f
		}
	case string:
		var parsed float64
		if n, err := fmt.Sscanf(x, "%f", &parsed); err == nil && n == 1 {
			return parsed
		}
	}
	return def
}
