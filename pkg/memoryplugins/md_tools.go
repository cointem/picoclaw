package memoryplugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/pkg/tools"
)

type memorySearchTool struct{ p *mdPlugin }

type memoryGetTool struct{ p *mdPlugin }

type memoryStoreTool struct{ p *mdPlugin }

func newMemorySearchTool(p *mdPlugin) tools.Tool { return &memorySearchTool{p: p} }
func newMemoryGetTool(p *mdPlugin) tools.Tool    { return &memoryGetTool{p: p} }
func newMemoryStoreTool(p *mdPlugin) tools.Tool  { return &memoryStoreTool{p: p} }

func (t *memorySearchTool) Name() string { return "memory_search" }

func (t *memorySearchTool) Description() string {
	return "在本地记忆文件（memory/MEMORY.md 与近期 daily notes）中按关键词检索相关片段。"
}

func (t *memorySearchTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "检索关键词/短语",
			},
			"days": map[string]any{
				"type":        "integer",
				"description": "检索最近多少天的 daily notes（默认 7）",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "返回最多多少条结果（默认 5）",
			},
			"max_chars": map[string]any{
				"type":        "integer",
				"description": "每条片段最大字符数（默认 240）",
			},
		},
		"required": []string{"query"},
	}
}

func (t *memorySearchTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return tools.ErrorResult("missing query")
	}

	days := intFromAny(args["days"], 7)
	maxResults := intFromAny(args["max_results"], 5)
	maxChars := intFromAny(args["max_chars"], 240)

	terms := extractQueryTerms(query)
	if len(terms) == 0 {
		terms = []string{query}
	}

	matches, err := t.p.searchTerms(terms, days, maxResults, maxChars)
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}

	payload := map[string]any{"matches": []map[string]any{}}
	out := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		out = append(out, map[string]any{
			"source":  m.Source,
			"snippet": m.Snippet,
		})
	}
	payload["matches"] = out
	b, _ := json.MarshalIndent(payload, "", "  ")
	return tools.SilentResult(string(b))
}

func (t *memoryGetTool) Name() string { return "memory_get" }

func (t *memoryGetTool) Description() string {
	return "读取本地记忆内容（MEMORY.md 与近期 daily notes），用于人工检查或调试。"
}

func (t *memoryGetTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"scope": map[string]any{
				"type":        "string",
				"description": "读取范围：all / long_term / daily_notes",
				"enum":        []string{"all", "long_term", "daily_notes"},
			},
			"days": map[string]any{
				"type":        "integer",
				"description": "读取最近多少天的 daily notes（默认 3）",
			},
			"max_chars": map[string]any{
				"type":        "integer",
				"description": "最多返回字符数（默认 8000，超出会截断）",
			},
		},
	}
}

func (t *memoryGetTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	scope, _ := args["scope"].(string)
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "all"
	}

	days := intFromAny(args["days"], 3)
	maxChars := intFromAny(args["max_chars"], 8000)

	sources, err := t.p.loadSources(days)
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}

	keys := make([]string, 0, len(sources))
	for k := range sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	switch scope {
	case "long_term":
		if v, ok := sources["MEMORY.md"]; ok {
			sb.WriteString(v)
		}
	case "daily_notes":
		for _, k := range keys {
			if k == "MEMORY.md" {
				continue
			}
			v := sources[k]
			if sb.Len() > 0 {
				sb.WriteString("\n\n---\n\n")
			}
			sb.WriteString("# ")
			sb.WriteString(k)
			sb.WriteString("\n\n")
			sb.WriteString(v)
		}
	default: // all
		if v, ok := sources["MEMORY.md"]; ok {
			sb.WriteString(v)
		}
		for _, k := range keys {
			if k == "MEMORY.md" {
				continue
			}
			v := sources[k]
			if sb.Len() > 0 {
				sb.WriteString("\n\n---\n\n")
			}
			sb.WriteString("# ")
			sb.WriteString(k)
			sb.WriteString("\n\n")
			sb.WriteString(v)
		}
	}

	text := sb.String()
	text = truncateRunes(text, maxChars)
	return tools.SilentResult(text)
}

func (t *memoryStoreTool) Name() string { return "memory_store" }

func (t *memoryStoreTool) Description() string {
	return "将一条可长期记住的信息写入 memory/MEMORY.md（或记录到今天的 daily note）。"
}

func (t *memoryStoreTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "要保存的内容",
			},
			"scope": map[string]any{
				"type":        "string",
				"description": "保存到 long_term 或 today（默认 long_term）",
				"enum":        []string{"long_term", "today"},
			},
		},
		"required": []string{"content"},
	}
}

func (t *memoryStoreTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	content, _ := args["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return tools.ErrorResult("missing content")
	}

	scope, _ := args["scope"].(string)
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "long_term"
	}

	switch scope {
	case "today":
		if err := t.p.appendToday(content); err != nil {
			return tools.ErrorResult(err.Error()).WithError(err)
		}
		return tools.SilentResult("stored to daily note")
	default:
		if err := t.p.appendLongTerm(content); err != nil {
			return tools.ErrorResult(err.Error()).WithError(err)
		}
		return tools.SilentResult("stored to MEMORY.md")
	}
}

func intFromAny(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i)
		}
	case string:
		var parsed int
		if n, err := fmt.Sscanf(x, "%d", &parsed); err == nil && n == 1 {
			return parsed
		}
	}
	return def
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "... (truncated)"
}
