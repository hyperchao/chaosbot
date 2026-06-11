// Package cli implements chaosbot's subcommand dispatch and
// per-subcommand handlers. The CLI struct is wired by the
// composition root (cmd/chaosbot/main.go) via di; tests build
// it the same way with hand-written fakes (per AGENTS.md:
// "no mock frameworks").
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"chaosbot/internal/agent"
	"chaosbot/internal/config"
)

// CLI is the wired-up command-line surface. All fields are
// populated by the di library via `di:"type"` or
// `di:"alias:..."` tags; the composition root (and tests)
// register the corresponding factory functions. The "out" /
// "errout" / "in" / "version" aliases let us register distinct
// instances of common types (io.Writer, io.Reader, string) by
// name.
type CLI struct {
	Agent   agent.Agent    `di:"type"`
	Config  *config.Config `di:"type"`
	In      io.Reader      `di:"alias:in"`
	Out     io.Writer      `di:"alias:out"`
	ErrOut  io.Writer      `di:"alias:errout"`
	Version string         `di:"alias:version"`
}

// Run dispatches to the named subcommand. Returns the error
// to be printed + used as the exit code by main. With no args,
// starts the REPL.
func (c *CLI) Run(args []string) error {
	if len(args) == 0 {
		return c.replCmd()
	}
	switch args[0] {
	case "run":
		return c.runCmd(args[1:])
	case "config":
		return c.configCmd(args[1:])
	case "version":
		return c.versionCmd(args[1:])
	default:
		return fmt.Errorf("unknown subcommand: %s (try 'run', 'config', 'version')", args[0])
	}
}

func (c *CLI) runCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("run: missing prompt argument")
	}
	if c.Agent == nil {
		return errors.New("run: no agent available (config not loaded — set --config or env vars like CHAOSBOT_API_KEY)")
	}
	prompt := args[0]
	answer, err := c.Agent.Run(context.Background(), prompt)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Out, answer)
	return nil
}

func (c *CLI) configCmd(args []string) error {
	if c.Config == nil {
		return errors.New("config: no config loaded (set --config or env vars like CHAOSBOT_API_KEY)")
	}
	cfg := c.Config
	fmt.Fprintf(c.Out, "provider:    %s\n", cfg.Provider.Name)
	fmt.Fprintf(c.Out, "model:       %s\n", cfg.Provider.Model)
	fmt.Fprintf(c.Out, "base_url:    %s\n", cfg.Provider.BaseURL)
	fmt.Fprintf(c.Out, "api_key:     %s\n", maskKey(cfg.Provider.APIKey))
	fmt.Fprintf(c.Out, "org_id:      %s\n", cfg.Provider.OrgID)
	if cfg.Provider.Timeout == 0 {
		fmt.Fprintln(c.Out, "timeout:     (default)")
	} else {
		fmt.Fprintf(c.Out, "timeout:     %s\n", cfg.Provider.Timeout)
	}
	fmt.Fprintf(c.Out, "system:      %s\n", cfg.System)
	fmt.Fprintf(c.Out, "max_steps:   %d\n", cfg.MaxSteps)
	fmt.Fprintf(c.Out, "temperature: %v\n", cfg.Temperature)
	fmt.Fprintf(c.Out, "max_tokens:  %d\n", cfg.MaxTokens)
	fmt.Fprintf(c.Out, "workspace:   %s\n", cfg.Workspace)
	return nil
}

func (c *CLI) versionCmd(args []string) error {
	fmt.Fprintf(c.Out, "chaosbot %s\n", c.Version)
	return nil
}

// replCmd drives the read-eval-print loop. History lives in
// the agent; the CLI just dispatches each line to Agent.Run
// and prints the reply. Slash commands: /reset calls
// Agent.Reset, /exit returns nil, /help prints the available
// commands. EOF (empty input) is treated like /exit.
func (c *CLI) replCmd() error {
	if c.Agent == nil {
		return errors.New("repl: no agent available (config not loaded)")
	}
	fmt.Fprintln(c.Out, "chaosbot REPL — type '/help' for commands, Ctrl-D to exit")
	scanner := bufio.NewScanner(c.In)
	for {
		fmt.Fprint(c.Out, "> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("repl: read input: %w", err)
			}
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "/reset":
			c.Agent.Reset()
			fmt.Fprintln(c.Out, "history cleared")
			continue
		case "/exit", "/quit":
			return nil
		case "/help":
			fmt.Fprintln(c.Out, "commands:")
			fmt.Fprintln(c.Out, "  /reset    clear conversation history")
			fmt.Fprintln(c.Out, "  /exit     leave the REPL (alias: /quit)")
			fmt.Fprintln(c.Out, "  /help     show this message")
			continue
		}
		reply, err := c.Agent.Run(context.Background(), line)
		if err != nil {
			fmt.Fprintln(c.ErrOut, "error:", err)
			continue
		}
		fmt.Fprintln(c.Out, reply)
	}
}

// maskKey redacts the middle of an API key for display: the
// first 4 and last 4 chars stay, the rest becomes "...".
func maskKey(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
