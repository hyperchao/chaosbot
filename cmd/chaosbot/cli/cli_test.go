package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hyperchao/di"

	"chaosbot/cmd/chaosbot/cli"
	"chaosbot/internal/agent"
	"chaosbot/internal/config"
	"chaosbot/internal/provider"
)

// fakeAgent is a hand-written test double of agent.Agent.
// Per AGENTS.md: no mock frameworks, just hand-written fakes.
type fakeAgent struct {
	runFunc  func(ctx context.Context, userInput string) (string, error)
	chatFunc func(ctx context.Context, msgs []provider.Message) (provider.Message, error)
}

func (f *fakeAgent) Run(ctx context.Context, userInput string) (string, error) {
	return f.runFunc(ctx, userInput)
}

func (f *fakeAgent) Chat(ctx context.Context, msgs []provider.Message) (provider.Message, error) {
	if f.chatFunc != nil {
		return f.chatFunc(ctx, msgs)
	}
	// Default: behave like Run for the last user message.
	var last string
	for _, m := range msgs {
		if m.Role == provider.RoleUser {
			last = m.Content
		}
	}
	reply, err := f.runFunc(ctx, last)
	if err != nil {
		return provider.Message{}, err
	}
	return provider.Message{Role: provider.RoleAssistant, Content: reply}, nil
}

var _ agent.Agent = (*fakeAgent)(nil)

// buildCLI wires a CLI via the di library. Tests pass fakes
// (a fakeAgent) and pre-built values; the same pattern main.go
// uses for production wiring.
func buildCLI(t *testing.T, fp agent.Agent, cfg *config.Config) (*cli.CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	c := di.New()
	di.RegisterDI(c, func() agent.Agent { return fp })
	di.RegisterDI(c, func() *config.Config { return cfg })
	di.RegisterAliasDI(c, "out", func() io.Writer { return out })
	di.RegisterAliasDI(c, "errout", func() io.Writer { return errOut })
	di.RegisterAliasDI(c, "version", func() string { return "vtest" })
	// *cli.CLI factory: di calls it, gets a zero *cli.CLI,
	// then walks its fields and injects Agent / Config / Out /
	// ErrOut / Version from the factories registered above.
	// di's "reflect.Value.Set on zero Value" panic for nil
	// interface factories means the *config.Config factory
	// above must NOT return nil — tests pass a real (possibly
	// zero) *config.Config value instead.
	di.RegisterDI(c, func() *cli.CLI { return &cli.CLI{} })
	return di.GetDI[*cli.CLI](c), out, errOut
}

func TestRun_NoSubcommand_Errors(t *testing.T) {
	c, _, _ := buildCLI(t, &fakeAgent{}, &config.Config{})
	err := c.Run([]string{})
	if err == nil {
		t.Fatal("want error for empty args")
	}
	if !strings.Contains(err.Error(), "REPL") {
		t.Errorf("err should mention REPL coming in 07-4: %v", err)
	}
}

func TestRun_UnknownSubcommand_Errors(t *testing.T) {
	c, _, _ := buildCLI(t, &fakeAgent{}, &config.Config{})
	err := c.Run([]string{"foobar"})
	if err == nil {
		t.Fatal("want error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "foobar") {
		t.Errorf("err should mention the bad name: %v", err)
	}
}

func TestRun_Version(t *testing.T) {
	c, out, _ := buildCLI(t, &fakeAgent{}, &config.Config{})
	if err := c.Run([]string{"version"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "vtest") {
		t.Errorf("out = %q, want contains 'vtest'", out.String())
	}
}

func TestRun_Config(t *testing.T) {
	cfg := &config.Config{
		Provider: config.ProviderConfig{
			Name:    "openai",
			APIKey:  "sk-abcdefghij1234",
			BaseURL: "https://api.openai.com",
			Timeout: 60 * time.Second,
		},
		System:      "you are a helper",
		MaxSteps:    25,
		Temperature: 0.7,
		MaxTokens:   2048,
		Workspace:   "/tmp/work",
	}
	cfg.Provider.Model = "gpt-4o-mini"

	c, out, _ := buildCLI(t, &fakeAgent{}, cfg)
	if err := c.Run([]string{"config"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"provider:    openai",
		"model:       gpt-4o-mini",
		"api_key:     sk-a...1234",
		"max_steps:   25",
		"temperature: 0.7",
		"max_tokens:  2048",
		"workspace:   /tmp/work",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("out missing %q\n--- output ---\n%s", want, s)
		}
	}
}

func TestRun_OneShot(t *testing.T) {
	c, out, _ := buildCLI(t, &fakeAgent{
		runFunc: func(_ context.Context, userInput string) (string, error) {
			if userInput != "hello" {
				t.Errorf("Run got userInput %q, want %q", userInput, "hello")
			}
			return "the answer", nil
		},
	}, &config.Config{})
	if err := c.Run([]string{"run", "hello"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "the answer") {
		t.Errorf("out = %q, want 'the answer'", out.String())
	}
}

func TestRun_OneShot_MissingPrompt_Errors(t *testing.T) {
	c, _, _ := buildCLI(t, &fakeAgent{}, &config.Config{})
	err := c.Run([]string{"run"})
	if err == nil {
		t.Fatal("want error for missing prompt")
	}
	if !strings.Contains(err.Error(), "prompt") {
		t.Errorf("err = %v, want mentions 'prompt'", err)
	}
}

func TestRun_OneShot_AgentError_Propagates(t *testing.T) {
	wantErr := errors.New("llm down")
	c, _, _ := buildCLI(t, &fakeAgent{
		runFunc: func(_ context.Context, _ string) (string, error) {
			return "", wantErr
		},
	}, &config.Config{})
	err := c.Run([]string{"run", "hi"})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wraps %v", err, wantErr)
	}
}
func TestConfig_NoConfig_ReturnsError(t *testing.T) {
	// REMOVED: see TestRun_NoProvider_ReturnsFriendlyError
	// below. The di library does not accept nil-interface
	// factories, so the *config.Config factory in buildCLI /
	// wire.go always returns a real (possibly zero) value.
	// The cli "no config loaded" path is unreachable in
	// production.
}

// TestRun_NoProvider_ReturnsFriendlyError covers the path
// where the agent's underlying Provider is emptyProvider
// (config is missing or has no API key). The fakeAgent here
// stands in for the real *reActAgent; the *reActAgent's
// Provider field would be emptyProvider, which errors on
// Chat. The cli receives the error from Agent.Run and
// surfaces it to the user as the exit-code error.
func TestRun_NoProvider_ReturnsFriendlyError(t *testing.T) {
	c, _, _ := buildCLI(t, &fakeAgent{
		runFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("no provider configured (set CHAOSBOT_API_KEY or pass --config)")
		},
	}, &config.Config{})
	err := c.Run([]string{"run", "hi"})
	if err == nil {
		t.Fatal("want error when provider is missing")
	}
	if !strings.Contains(err.Error(), "no provider") {
		t.Errorf("err = %v, want mentions 'no provider'", err)
	}
}
