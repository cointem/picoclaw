package memoryplugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type mem0Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	orgID      string
	projectID  string
}

func newMem0Client(apiKey, baseURL, orgID, projectID string) (*mem0Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("missing api key")
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "https://api.mem0.ai"
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}

	return &mem0Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 25 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Mem0 endpoints may respond with 301/302 redirects depending on
				// edge/proxy normalization. net/http converts POST to GET for
				// 301/302/303 by default; preserve the original method/body.
				if len(via) == 0 {
					return nil
				}
				orig := via[0]
				if orig.Method != "" {
					req.Method = orig.Method
				}
				if orig.GetBody != nil {
					body, err := orig.GetBody()
					if err != nil {
						return err
					}
					req.Body = body
					req.ContentLength = orig.ContentLength
				}
				// Preserve headers (incl. Authorization) unless net/http already did.
				for k, vv := range orig.Header {
					if _, exists := req.Header[k]; !exists {
						for _, v := range vv {
							req.Header.Add(k, v)
						}
					}
				}
				return nil
			},
		},
		orgID:     strings.TrimSpace(orgID),
		projectID: strings.TrimSpace(projectID),
	}, nil
}

type mem0Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type mem0AddRequest struct {
	UserID       string         `json:"user_id,omitempty"`
	Messages     []mem0Message  `json:"messages,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	Infer        *bool          `json:"infer,omitempty"`
	AsyncMode    *bool          `json:"async_mode,omitempty"`
	OutputFormat string         `json:"output_format,omitempty"`
	Version      string         `json:"version,omitempty"`
	OrgID        string         `json:"org_id,omitempty"`
	ProjectID    string         `json:"project_id,omitempty"`
}

type mem0AddEvent struct {
	ID    string `json:"id"`
	Event string `json:"event"`
	Data  struct {
		Memory string `json:"memory"`
	} `json:"data"`
}

type mem0SearchRequest struct {
	Query      string         `json:"query"`
	Filters    any            `json:"filters"`
	Version    string         `json:"version,omitempty"`
	TopK       int            `json:"top_k,omitempty"`
	Threshold  *float64       `json:"threshold,omitempty"`
	Fields     []string       `json:"fields,omitempty"`
	OrgID      string         `json:"org_id,omitempty"`
	ProjectID  string         `json:"project_id,omitempty"`
	Rerank     *bool          `json:"rerank,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Keyword    *bool          `json:"keyword_search,omitempty"`
	FilterOnly *bool          `json:"filter_memories,omitempty"`
}

type mem0Memory struct {
	ID             string         `json:"id"`
	Memory         string         `json:"memory"`
	UserID         string         `json:"user_id"`
	Categories     []string       `json:"categories,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      string         `json:"created_at,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
	Immutable      bool           `json:"immutable,omitempty"`
	ExpirationDate any            `json:"expiration_date,omitempty"`
	Score          *float64       `json:"score,omitempty"`
}

func (c *mem0Client) Add(ctx context.Context, req mem0AddRequest) ([]mem0AddEvent, error) {
	if req.OrgID == "" {
		req.OrgID = c.orgID
	}
	if req.ProjectID == "" {
		req.ProjectID = c.projectID
	}
	b, err := c.doRaw(ctx, http.MethodPost, "/v1/memories/", req)
	if err != nil {
		return nil, err
	}

	var out []mem0AddEvent
	if err := json.Unmarshal(b, &out); err == nil {
		return out, nil
	}
	var wrapped struct {
		Results []mem0AddEvent `json:"results"`
	}
	if err := json.Unmarshal(b, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.Results, nil
}

func (c *mem0Client) Search(ctx context.Context, req mem0SearchRequest) ([]mem0Memory, error) {
	if req.OrgID == "" {
		req.OrgID = c.orgID
	}
	if req.ProjectID == "" {
		req.ProjectID = c.projectID
	}
	b, err := c.doRaw(ctx, http.MethodPost, "/v2/memories/search", req)
	if err != nil {
		return nil, err
	}

	var out []mem0Memory
	if err := json.Unmarshal(b, &out); err == nil {
		return out, nil
	}
	var wrapped struct {
		Results []mem0Memory `json:"results"`
	}
	if err := json.Unmarshal(b, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.Results, nil
}

func (c *mem0Client) Get(ctx context.Context, memoryID string) (*mem0Memory, error) {
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return nil, fmt.Errorf("missing memory_id")
	}
	var out mem0Memory
	if err := c.doJSON(ctx, http.MethodGet, "/v1/memories/"+url.PathEscape(memoryID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *mem0Client) Update(ctx context.Context, memoryID, text string, metadata map[string]any) (*mem0Memory, error) {
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return nil, fmt.Errorf("missing memory_id")
	}
	payload := map[string]any{}
	if strings.TrimSpace(text) != "" {
		payload["text"] = text
	}
	if metadata != nil {
		payload["metadata"] = metadata
	}
	var out mem0Memory
	if err := c.doJSON(ctx, http.MethodPut, "/v1/memories/"+url.PathEscape(memoryID), payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *mem0Client) Delete(ctx context.Context, memoryID string) error {
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return fmt.Errorf("missing memory_id")
	}
	_, err := c.doRaw(ctx, http.MethodDelete, "/v1/memories/"+url.PathEscape(memoryID), nil)
	return err
}

func (c *mem0Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	b, err := c.doRaw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if len(b) == 0 {
		return fmt.Errorf("empty response")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return dec.Decode(out)
}

func (c *mem0Client) doRaw(ctx context.Context, method, path string, body any) ([]byte, error) {
	fullURL := c.baseURL + path
	var reqBody io.Reader
	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyBytes = b
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBytes))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("mem0 api %s %s: %s", method, path, msg)
	}
	return respBytes, nil
}
