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
	"net/http"
	"time"

	openaipkg "github.com/sashabaranov/go-openai"

	"chaosbot/internal/provider"
)

const (
	defaultTimeout = 60 * time.Second
	defaultName    = "openai"
)

// Provider implements provider.Provider using the OpenAI SDK. The
// underlying *http.Client is reused across Chat calls; see Config.
type Provider struct {
	client *openaipkg.Client
	name   string
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
	return &Provider{client: openaipkg.NewClientWithConfig(sdkCfg), name: name}
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return p.name }

// Chat implements provider.Provider. The provider.Request is mapped
// to the SDK's chat-completion request; the SDK's response is mapped
// back into provider.Response.
func (p *Provider) Chat(ctx context.Context, req provider.Request) (*provider.Response, error) {
	oaiReq, err := toOpenAIRequest(req)
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	oaiResp, err := p.client.CreateChatCompletion(ctx, oaiReq)
	if err != nil {
		return nil, fmt.Errorf("openai: chat completion: %w", err)
	}
	return fromOpenAIResponse(&oaiResp)
}

func timeoutOrDefault(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return defaultTimeout
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
				Function: &openaipkg.FunctionDefine{
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
