// Package openai implements provider.Provider against the OpenAI chat
// completions protocol. The same protocol is spoken by OpenAI,
// DeepSeek, GLM, vLLM, Ollama, and any vendor that serves
// /v1/chat/completions. Switch provider.Config.BaseURL to point at
// any of them.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	openaipkg "github.com/sashabaranov/go-openai"

	"chaosbot/internal/provider"
)

const (
	defaultTimeout        = 60 * time.Second
	defaultName           = "openai"
	defaultMaxRetries     = 3
	defaultRetryBaseDelay = 1 * time.Second
)

// Provider implements provider.Provider using the OpenAI SDK. The
// underlying *http.Client is reused across Chat calls; see Config.
type Provider struct {
	client         *openaipkg.Client
	name           string
	maxRetries     int
	retryBaseDelay time.Duration
}

// New returns a provider.Provider backed by the configured endpoint.
// It satisfies provider.Provider (compile-time asserted in the test
// file when one is added).
func New(cfg provider.Config) provider.Provider {
	sdkCfg := openaipkg.DefaultConfig(cfg.APIKey)
	sdkCfg.OrgID = cfg.OrgID
	if cfg.BaseURL != "" {
		sdkCfg.BaseURL = cfg.BaseURL
	}
	sdkCfg.HTTPClient = &http.Client{Timeout: timeoutOrDefault(cfg.Timeout)}
	name := cfg.Name
	if name == "" {
		name = defaultName
	}
	return &Provider{
		client:         openaipkg.NewClientWithConfig(sdkCfg),
		name:           name,
		maxRetries:     retryOrDefault(cfg.MaxRetries),
		retryBaseDelay: retryBaseOrDefault(cfg.RetryBaseDelay),
	}
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return p.name }

// EstimateTokens implements provider.Provider. Uses the
// default heuristic — OpenAI's real tokenizer is the
// tiktoken algorithm which would add a 5 MB binary for
// marginal accuracy gain over the 10% safety buffer.
func (p *Provider) EstimateTokens(content string) int { return provider.EstimateTokensDefault(content) }

// Chat implements provider.Provider. The provider.Request is mapped
// to the SDK's chat-completion request; the SDK's response is mapped
// back into provider.Response. SDK errors are classified into the
// provider's error sentinels (ErrRateLimited, ErrAuthFailed, etc.)
// so callers can branch on error category. Transient errors
// (rate limit, server error, network) are retried with exponential
// backoff and jitter; permanent errors (auth, bad request) are
// returned immediately.
func (p *Provider) Chat(ctx context.Context, req provider.Request) (*provider.Response, error) {
	oaiReq, err := toOpenAIRequest(req)
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		oaiResp, err := p.client.CreateChatCompletion(ctx, oaiReq)
		if err == nil {
			return fromOpenAIResponse(&oaiResp)
		}
		classified := classifyOpenAIError(err)
		lastErr = classified
		if !isRetryable(classified) {
			return nil, classified
		}
		if attempt == p.maxRetries {
			break
		}
		delay := backoffWithJitter(attempt, p.retryBaseDelay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}

// isRetryable returns true for transient errors (rate limit,
// server error, network). Permanent errors (auth, bad
// request) return false and are surfaced to the caller.
func isRetryable(err error) bool {
	return errors.Is(err, provider.ErrRateLimited) ||
		errors.Is(err, provider.ErrServerError) ||
		errors.Is(err, provider.ErrNetwork)
}

// backoffWithJitter returns the delay before retrying after
// `attempt` failures (0-indexed). Strategy: base * 2^attempt
// + uniform random in [0, base). The jitter spreads retries
// when many clients hit the same 429 burst.
//
// example: base=1s, attempt=0 → 1s + [0,1s) = 1-2s
//
//	attempt=1 → 2s + [0,1s) = 2-3s
//	attempt=2 → 4s + [0,1s) = 4-5s
func backoffWithJitter(attempt int, base time.Duration) time.Duration {
	if base <= 0 {
		base = defaultRetryBaseDelay
	}
	exp := base << attempt // base * 2^attempt; overflow OK, time.Duration is int64
	if exp <= 0 || exp > 60*time.Second {
		exp = 60 * time.Second
	}
	jitter := time.Duration(rand.Int63n(int64(base)))
	return exp + jitter
}

// classifyOpenAIError maps the SDK's error types to the
// provider's error sentinels. HTTP status codes follow the
// spec; non-APIError (network, timeout) becomes ErrNetwork.
// The original error message is wrapped so users still see
// details (e.g. the "rate limit" reason), but errors.Is
// callers can branch on the sentinel.
func classifyOpenAIError(err error) error {
	var apiErr *openaipkg.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.HTTPStatusCode == http.StatusTooManyRequests:
			return fmt.Errorf("%w: %s", provider.ErrRateLimited, apiErr.Error())
		case apiErr.HTTPStatusCode == http.StatusUnauthorized,
			apiErr.HTTPStatusCode == http.StatusForbidden:
			return fmt.Errorf("%w: %s", provider.ErrAuthFailed, apiErr.Error())
		case apiErr.HTTPStatusCode >= 500 && apiErr.HTTPStatusCode < 600:
			return fmt.Errorf("%w: %s", provider.ErrServerError, apiErr.Error())
		case apiErr.HTTPStatusCode == http.StatusBadRequest:
			// Could also be ErrContextLength, but the SDK
			// doesn't distinguish; 04-4b safety net handles
			// 400s from context length separately by string
			// match in the agent loop.
			return fmt.Errorf("%w: %s", provider.ErrBadRequest, apiErr.Error())
		default:
			return fmt.Errorf("%w: status %d: %s", provider.ErrBadRequest, apiErr.HTTPStatusCode, apiErr.Error())
		}
	}
	// Non-APIError: network failure, timeout, DNS, etc.
	return fmt.Errorf("%w: %s", provider.ErrNetwork, err.Error())
}

func timeoutOrDefault(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return defaultTimeout
}

func retryOrDefault(n int) int {
	if n >= 0 {
		return n
	}
	return defaultMaxRetries
}

func retryBaseOrDefault(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return defaultRetryBaseDelay
}

// toOpenAIRequest converts our internal Request to the SDK's request.
// System is emitted as a leading system message so the wire format
// is a flat message list.
func toOpenAIRequest(req provider.Request) (openaipkg.ChatCompletionRequest, error) {
	msgs := make([]openaipkg.ChatCompletionMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, openaipkg.ChatCompletionMessage{
			Role:    openaipkg.ChatMessageRoleSystem,
			Content: req.System,
		})
	}
	for _, m := range req.Messages {
		converted, err := toOpenAIMessage(m)
		if err != nil {
			return openaipkg.ChatCompletionRequest{}, err
		}
		msgs = append(msgs, converted)
	}
	oaiReq := openaipkg.ChatCompletionRequest{
		Model:    req.Model,
		Messages: msgs,
	}
	if req.Temperature > 0 {
		oaiReq.Temperature = float32(req.Temperature)
	}
	if req.MaxTokens > 0 {
		oaiReq.MaxTokens = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		oaiReq.Tools = make([]openaipkg.Tool, len(req.Tools))
		for i, t := range req.Tools {
			oaiReq.Tools[i] = openaipkg.Tool{
				Type: openaipkg.ToolTypeFunction,
				Function: &openaipkg.FunctionDefinition{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			}
		}
	}
	return oaiReq, nil
}

func toOpenAIMessage(m provider.Message) (openaipkg.ChatCompletionMessage, error) {
	switch m.Role {
	case provider.RoleUser:
		return openaipkg.ChatCompletionMessage{
			Role:    openaipkg.ChatMessageRoleUser,
			Content: m.Content,
		}, nil
	case provider.RoleAssistant:
		out := openaipkg.ChatCompletionMessage{
			Role:    openaipkg.ChatMessageRoleAssistant,
			Content: m.Content,
		}
		if len(m.ToolCalls) > 0 {
			out.ToolCalls = make([]openaipkg.ToolCall, len(m.ToolCalls))
			for i, c := range m.ToolCalls {
				out.ToolCalls[i] = openaipkg.ToolCall{
					ID:   c.ID,
					Type: openaipkg.ToolTypeFunction,
					Function: openaipkg.FunctionCall{
						Name:      c.Name,
						Arguments: string(c.Arguments),
					},
				}
			}
		}
		return out, nil
	case provider.RoleTool:
		return openaipkg.ChatCompletionMessage{
			Role:       openaipkg.ChatMessageRoleTool,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}, nil
	case provider.RoleSystem:
		return openaipkg.ChatCompletionMessage{
			Role:    openaipkg.ChatMessageRoleSystem,
			Content: m.Content,
		}, nil
	default:
		return openaipkg.ChatCompletionMessage{}, fmt.Errorf("unknown role: %q", m.Role)
	}
}

func fromOpenAIResponse(r *openaipkg.ChatCompletionResponse) (*provider.Response, error) {
	if r == nil || len(r.Choices) == 0 {
		return nil, errors.New("openai: response has no choices")
	}
	choice := r.Choices[0]
	out := &provider.Response{Content: choice.Message.Content}
	for _, c := range choice.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, provider.ToolCall{
			ID:        c.ID,
			Name:      c.Function.Name,
			Arguments: json.RawMessage(c.Function.Arguments),
		})
	}
	if r.Usage.PromptTokens != 0 || r.Usage.CompletionTokens != 0 || r.Usage.TotalTokens != 0 {
		out.Usage = provider.Usage{
			PromptTokens:     r.Usage.PromptTokens,
			CompletionTokens: r.Usage.CompletionTokens,
			TotalTokens:      r.Usage.TotalTokens,
		}
	}
	return out, nil
}
