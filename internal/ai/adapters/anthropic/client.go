// Package anthropic implements the ai.Client port using the Anthropic Messages API.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/1xxn/getafjob/internal/ai"
)

const defaultBaseURL = "https://api.anthropic.com"

// Client calls the Anthropic Messages API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

var _ ai.Client = (*Client)(nil)

// Option configures the Anthropic Client.
type Option func(*Client)

// WithBaseURL overrides the API endpoint (useful for tests).
func WithBaseURL(url string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(url, "/")
	}
}

// WithHTTPClient overrides the underlying HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// NewClient returns a new Anthropic API client.
func NewClient(opts ...Option) *Client {
	c := &Client{
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Tool represents an Anthropic tool definition for forced structured output.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type toolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiRequest struct {
	Model      string      `json:"model"`
	MaxTokens  int         `json:"max_tokens"`
	System     string      `json:"system,omitempty"`
	Messages   []message   `json:"messages"`
	Tools      []Tool      `json:"tools,omitempty"`
	ToolChoice *toolChoice `json:"tool_choice,omitempty"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type apiResponse struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
	Model   string         `json:"model"`
	Usage   usage          `json:"usage"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) call(ctx context.Context, apiKey string, req apiRequest) (*apiResponse, error) {
	if apiKey == "" {
		return nil, ai.ErrNoAPIKey
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		var apiErr apiResponse
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Error != nil {
			return nil, fmt.Errorf("anthropic api error (%d %s): %s", resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("anthropic api error (%d): %s", resp.StatusCode, string(respBody))
	}

	var res apiResponse
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("%w: %v", ai.ErrInvalidSchema, err)
	}
	return &res, nil
}

func findToolInput(res *apiResponse, toolName string) (json.RawMessage, error) {
	for _, block := range res.Content {
		if block.Type == "tool_use" && (toolName == "" || block.Name == toolName) {
			return block.Input, nil
		}
	}
	return nil, fmt.Errorf("%w: missing tool_use output for %s", ai.ErrInvalidSchema, toolName)
}

// CompletePlan implements ai.Client.
func (c *Client) CompletePlan(ctx context.Context, apiKey, model string, req ai.PlanRequest) (ai.PlanResult, int, int, error) {
	tool := Tool{
		Name:        "submit_plan",
		Description: "Submit proposed values for application form fields with provenance",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"explanation": map[string]any{"type": "string"},
				"fields": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"field_id":   map[string]any{"type": "string"},
							"value":      map[string]any{"type": "string"},
							"provenance": map[string]any{"type": "string"},
							"confidence": map[string]any{"type": "number"},
						},
						"required": []string{"field_id", "value"},
					},
				},
			},
			"required": []string{"fields"},
		},
	}

	promptJSON, _ := json.Marshal(req)
	sys := "You are an intelligent form planner. Analyze the form fields and map them to verified user facts. Never invent information."

	apiResp, err := c.call(ctx, apiKey, apiRequest{
		Model:      model,
		MaxTokens:  2048,
		System:     sys,
		Messages:   []message{{Role: "user", Content: string(promptJSON)}},
		Tools:      []Tool{tool},
		ToolChoice: &toolChoice{Type: "tool", Name: "submit_plan"},
	})
	if err != nil {
		return ai.PlanResult{}, 0, 0, err
	}

	rawInput, err := findToolInput(apiResp, "submit_plan")
	if err != nil {
		return ai.PlanResult{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, err
	}

	var plan ai.PlanResult
	if err := json.Unmarshal(rawInput, &plan); err != nil {
		return ai.PlanResult{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, fmt.Errorf("%w: %v", ai.ErrInvalidSchema, err)
	}
	return plan, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, nil
}

// CompleteDraft implements ai.Client.
func (c *Client) CompleteDraft(ctx context.Context, apiKey, model string, req ai.DraftRequest) (ai.DraftResult, int, int, error) {
	tool := Tool{
		Name:        "submit_draft",
		Description: "Submit an answer draft for an application question",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"draft_text": map[string]any{"type": "string"},
				"facts_used": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
			"required": []string{"draft_text"},
		},
	}

	promptJSON, _ := json.Marshal(req)
	sys := "You draft answers to job application questions based strictly on the user's verified facts and work experience. Do not fabricate experience. Never draft cover letters."

	apiResp, err := c.call(ctx, apiKey, apiRequest{
		Model:      model,
		MaxTokens:  1024,
		System:     sys,
		Messages:   []message{{Role: "user", Content: string(promptJSON)}},
		Tools:      []Tool{tool},
		ToolChoice: &toolChoice{Type: "tool", Name: "submit_draft"},
	})
	if err != nil {
		return ai.DraftResult{}, 0, 0, err
	}

	rawInput, err := findToolInput(apiResp, "submit_draft")
	if err != nil {
		return ai.DraftResult{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, err
	}

	var draft ai.DraftResult
	if err := json.Unmarshal(rawInput, &draft); err != nil {
		return ai.DraftResult{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, fmt.Errorf("%w: %v", ai.ErrInvalidSchema, err)
	}
	draft.FieldID = req.Field.ID
	draft.RequiresApproval = true
	return draft, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, nil
}

// CompleteExtract implements ai.Client.
func (c *Client) CompleteExtract(ctx context.Context, apiKey, model string, req ai.ExtractRequest) (ai.ExtractResult, int, int, error) {
	tool := Tool{
		Name:        "submit_extraction",
		Description: "Submit structured job posting details extracted from text",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":           map[string]any{"type": "string"},
				"company":         map[string]any{"type": "string"},
				"salary_min":      map[string]any{"type": "integer"},
				"salary_max":      map[string]any{"type": "integer"},
				"salary_currency": map[string]any{"type": "string"},
				"salary_interval": map[string]any{"type": "string", "enum": []string{"yearly", "monthly", "hourly"}},
				"location":        map[string]any{"type": "string"},
				"remote":          map[string]any{"type": "boolean"},
				"skills": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"employment_type": map[string]any{"type": "string"},
			},
			"required": []string{"title", "company"},
		},
	}

	content := req.Text
	if content == "" {
		content = req.HTML
	}
	sys := "Extract structured job posting data accurately. If a field is not mentioned, omit it."

	apiResp, err := c.call(ctx, apiKey, apiRequest{
		Model:      model,
		MaxTokens:  1024,
		System:     sys,
		Messages:   []message{{Role: "user", Content: content}},
		Tools:      []Tool{tool},
		ToolChoice: &toolChoice{Type: "tool", Name: "submit_extraction"},
	})
	if err != nil {
		return ai.ExtractResult{}, 0, 0, err
	}

	rawInput, err := findToolInput(apiResp, "submit_extraction")
	if err != nil {
		return ai.ExtractResult{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, err
	}

	var ext ai.ExtractResult
	if err := json.Unmarshal(rawInput, &ext); err != nil {
		return ai.ExtractResult{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, fmt.Errorf("%w: %v", ai.ErrInvalidSchema, err)
	}
	return ext, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, nil
}

// CompleteClassify implements ai.Client.
func (c *Client) CompleteClassify(ctx context.Context, apiKey, model string, req ai.ClassifyRequest) (ai.ClassifyResult, int, int, error) {
	tool := Tool{
		Name:        "submit_classification",
		Description: "Submit classification of applicant communication",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"category": map[string]any{
					"type": "string",
					"enum": []string{
						string(ai.CategoryRejection),
						string(ai.CategoryInvitation),
						string(ai.CategoryInterview),
						string(ai.CategoryOffer),
						string(ai.CategoryStatusUpdate),
						string(ai.CategoryUnrelated),
					},
				},
				"confidence": map[string]any{"type": "number"},
				"reasoning":  map[string]any{"type": "string"},
			},
			"required": []string{"category", "confidence"},
		},
	}

	input := fmt.Sprintf("Subject: %s\nFrom: %s\n\n%s", req.Subject, req.Sender, req.Body)
	sys := "Classify applicant communication emails into one of: rejection, invitation, interview, offer, status_update, unrelated."

	apiResp, err := c.call(ctx, apiKey, apiRequest{
		Model:      model,
		MaxTokens:  512,
		System:     sys,
		Messages:   []message{{Role: "user", Content: input}},
		Tools:      []Tool{tool},
		ToolChoice: &toolChoice{Type: "tool", Name: "submit_classification"},
	})
	if err != nil {
		return ai.ClassifyResult{}, 0, 0, err
	}

	rawInput, err := findToolInput(apiResp, "submit_classification")
	if err != nil {
		return ai.ClassifyResult{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, err
	}

	var cls ai.ClassifyResult
	if err := json.Unmarshal(rawInput, &cls); err != nil {
		return ai.ClassifyResult{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, fmt.Errorf("%w: %v", ai.ErrInvalidSchema, err)
	}
	if cls.Category == "" {
		return ai.ClassifyResult{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, errors.New("empty category returned")
	}
	return cls, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, nil
}
