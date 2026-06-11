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
)

// fakeAgent is a hand-written test double of agent.Agent.
// Per AGENTS.md: no mock frameworks, just hand-written fakes.
type fakeAgent struct {
	runFunc    func(ctx context.Context, userInput string) (string, error)
	resetCalls int
	// prompts captures every prompt passed to Run, so tests
	// can verify multi-turn ordering without the agent owning
	// history.
	prompts []string
}

func (f *fakeAgent) Run(ctx context.Context, userInput string) (string, error) {
	f.prompts = append(f.prompts, userInput)
	return f.runFunc(ctx, userInput)
}

func (f *fakeAgent) Reset() {
	f.resetCalls++
}

var _ agent.Agent = (*fakeAgent)(nil)

// buildCLI wires a CLI via the di library. Tests pass fakes
// (a fakeAgent) and pre-built values; the same pattern main.go
// uses for production wiring. The `in` reader is registered
// even though most subcommand tests don't read from it — the
// di library requires every aliased field to resolve.
func buildCLI(t *testing.T, fp agent.Agent, cfg *config.Config) (*cli.CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	in := &bytes.Buffer{}
	c := di.New()
	di.RegisterDI(c, func() agent.Agent { return fp })
	di.RegisterDI(c, func() *config.Config { return cfg })
	di.RegisterAliasDI(c, "in", func() io.Reader { return in })
	di.RegisterAliasDI(c, "out", func() io.Writer { return out })
	di.RegisterAliasDI(c, "errout", func() io.Writer { return errOut })
	di.RegisterAliasDI(c, "version", func() string { return "vtest" })
	// *cli.CLI factory: di calls it, gets a zero *cli.CLI,
	// then walks its fields and injects Agent / Config / In /
	// Out / ErrOut / Version from the factories registered
	// above. di's "reflect.Value.Set on zero Value" panic
	// for nil interface factories means the *config.Config
	// factory above must NOT return nil — tests pass a real
	// (possibly zero) *config.Config value instead.
	di.RegisterDI(c, func() *cli.CLI { return &cli.CLI{} })
	return di.GetDI[*cli.CLI](c), out, errOut
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

// buildREPL wires a CLI whose In reader is the supplied buffer.
// Use this for tests that drive the REPL with programmed input.
func buildREPL(t *testing.T, fp agent.Agent, input string) (*cli.CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	in := bytes.NewBufferString(input)
	c := di.New()
	di.RegisterDI(c, func() agent.Agent { return fp })
	di.RegisterDI(c, func() *config.Config { return &config.Config{} })
	di.RegisterAliasDI(c, "in", func() io.Reader { return in })
	di.RegisterAliasDI(c, "out", func() io.Writer { return out })
	di.RegisterAliasDI(c, "errout", func() io.Writer { return errOut })
	di.RegisterAliasDI(c, "version", func() string { return "vtest" })
	di.RegisterDI(c, func() *cli.CLI { return &cli.CLI{} })
	return di.GetDI[*cli.CLI](c), out, errOut
}

// TestREPL_NoSubcommand_StartsREPL replaces the pre-07-4
// "no subcommand" error test. With 07-4 landed, an empty
// argv goes straight into the REPL.
func TestREPL_NoSubcommand_StartsREPL(t *testing.T) {
	c, out, _ := buildREPL(t, &fakeAgent{
		runFunc: func(_ context.Context, _ string) (string, error) {
			return "answer", nil
		},
	}, "hi\n/exit\n")
	if err := c.Run([]string{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "answer") {
		t.Errorf("out = %q, want contains 'answer'", out.String())
	}
	if !strings.Contains(out.String(), "REPL") {
		t.Errorf("out = %q, want contains 'REPL' banner", out.String())
	}
}

// TestREPL_TwoTurnLoop drives two user prompts and verifies
// that the fakeAgent's Run is invoked twice with the right
// prompts. History is owned by the agent under test (a real
// *reActAgent would feed back the prior exchange to the
// provider; we only need to confirm dispatch here).
func TestREPL_TwoTurnLoop(t *testing.T) {
	fa := &fakeAgent{
		runFunc: func(_ context.Context, prompt string) (string, error) {
			return "reply-to-" + prompt, nil
		},
	}
	c, out, _ := buildREPL(t, fa, "hi\nagain\n/exit\n")
	if err := c.Run([]string{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fa.prompts) != 2 {
		t.Fatalf("prompts len = %d, want 2", len(fa.prompts))
	}
	if fa.prompts[0] != "hi" || fa.prompts[1] != "again" {
		t.Errorf("prompts = %v, want [hi again]", fa.prompts)
	}
	if !strings.Contains(out.String(), "reply-to-hi") {
		t.Errorf("out = %q, want contains 'reply-to-hi'", out.String())
	}
	if !strings.Contains(out.String(), "reply-to-again") {
		t.Errorf("out = %q, want contains 'reply-to-again'", out.String())
	}
}

// TestREPL_SlashExit verifies /exit returns nil and skips the
// agent entirely.
func TestREPL_SlashExit(t *testing.T) {
	called := false
	c, _, _ := buildREPL(t, &fakeAgent{
		runFunc: func(_ context.Context, _ string) (string, error) {
			called = true
			return "", nil
		},
	}, "/exit\n")
	if err := c.Run([]string{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("agent should not be invoked after /exit")
	}
}

// TestREPL_SlashReset verifies /reset calls Agent.Reset. We
// can't observe what the agent does with that signal (history
// is internal) so we count the calls.
func TestREPL_SlashReset(t *testing.T) {
	fa := &fakeAgent{
		runFunc: func(_ context.Context, _ string) (string, error) {
			return "ok", nil
		},
	}
	c, out, _ := buildREPL(t, fa, "first\n/reset\nsecond\n/exit\n")
	if err := c.Run([]string{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fa.resetCalls != 1 {
		t.Errorf("Reset calls = %d, want 1", fa.resetCalls)
	}
	if len(fa.prompts) != 2 || fa.prompts[0] != "first" || fa.prompts[1] != "second" {
		t.Errorf("prompts = %v, want [first second]", fa.prompts)
	}
	if !strings.Contains(out.String(), "history cleared") {
		t.Errorf("out = %q, want contains 'history cleared'", out.String())
	}
}

// TestREPL_SlashHelp verifies /help prints the slash command
// list.
func TestREPL_SlashHelp(t *testing.T) {
	c, out, _ := buildREPL(t, &fakeAgent{}, "/help\n/exit\n")
	if err := c.Run([]string{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"/reset", "/exit", "/help"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out missing %q\n--- output ---\n%s", want, out.String())
		}
	}
}

// TestREPL_EOF_Exits verifies that an empty input (no lines)
// is treated as EOF and returns nil without invoking the
// agent.
func TestREPL_EOF_Exits(t *testing.T) {
	called := false
	c, _, _ := buildREPL(t, &fakeAgent{
		runFunc: func(_ context.Context, _ string) (string, error) {
			called = true
			return "", nil
		},
	}, "")
	if err := c.Run([]string{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("agent should not be invoked on EOF")
	}
}
