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
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"chaosbot/internal/agent"
	"chaosbot/internal/config"
	"chaosbot/internal/session"

	"github.com/peterh/liner" // why: pure-Go readline for REPL (history, line-editing, tab-completion); docker/geth also use it
	"golang.org/x/term"
)

// CLI is the wired-up command-line surface. All fields are
// populated by the di library via `di:"type"` or
// `di:"alias:..."` tags; the composition root (and tests)
// register the corresponding factory functions. The "out" /
// "errout" / "in" / "version" aliases let us register distinct
// instances of common types (io.Writer, io.Reader, string) by
// name.
//
// CLI does NOT know about session.Store; session persistence
// is owned by the agent (Phase 06). CLI just calls agent.Run
// / agent.Reset / agent.Resume; the agent handles save/load.
//
// As of Phase 11, CLI also uses session.Store to implement /sessions.
type CLI struct {
	Agent    agent.Agent     `di:"type"`
	Registry *agent.Registry `di:"type"`
	Config   *config.Config  `di:"type"`
	Store    session.Store   `di:"type"`
	In       io.Reader       `di:"alias:in"`
	Out      io.Writer       `di:"alias:out"`
	ErrOut   io.Writer       `di:"alias:errout"`
	Version  string          `di:"alias:version"`
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

// runCmd handles `chaosbot run [flags] <prompt>`. Flags:
//
//	--resume <id>  resume a saved session by id
func (c *CLI) runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)
	var sessionID string
	fs.StringVar(&sessionID, "resume", "", "resume a saved session by id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("run: missing prompt argument")
	}
	if c.Agent == nil {
		return errors.New("run: no agent available (config not loaded — set --config or env vars like CHAOSBOT_API_KEY)")
	}
	ctx := context.Background()
	if sessionID != "" {
		if err := c.Agent.Resume(ctx, sessionID); err != nil {
			return err
		}
	}
	reply, err := c.Agent.Run(ctx, rest[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Out, stripThink(reply))
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
	fmt.Fprintf(c.Out, "max_retries: %d\n", cfg.Provider.MaxRetries)
	if cfg.Provider.RetryBaseDelay == 0 {
		fmt.Fprintln(c.Out, "retry_base_delay: (default)")
	} else {
		fmt.Fprintf(c.Out, "retry_base_delay: %s\n", cfg.Provider.RetryBaseDelay)
	}
	fmt.Fprintf(c.Out, "system:      %s\n", cfg.System)
	fmt.Fprintf(c.Out, "max_steps:   %d\n", cfg.MaxSteps)
	fmt.Fprintf(c.Out, "temperature: %v\n", cfg.Temperature)
	fmt.Fprintf(c.Out, "max_tokens:  %d\n", cfg.MaxTokens)
	fmt.Fprintf(c.Out, "workspace:   %s\n", cfg.Workspace)
	fmt.Fprintf(c.Out, "sessions_dir: %s\n", cfg.SessionsDir)
	return nil
}

func (c *CLI) versionCmd(args []string) error {
	fmt.Fprintf(c.Out, "chaosbot %s\n", c.Version)
	return nil
}

type sessionItem struct {
	id      string
	summary string
}

type REPLIO interface {
	Prompt(string) (string, error)
}

func (c *CLI) sessionsCmd(io REPLIO) error {
	ctx := context.Background()

	if c.Store == nil {
		return errors.New("no session store configured (set sessions_dir in config)")
	}

	ids, err := c.Store.List(ctx)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if len(ids) == 0 {
		fmt.Fprintln(c.Out, "no saved sessions")
		return nil
	}

	items := make([]sessionItem, 0, len(ids))
	for _, id := range ids {
		summary, _ := c.Store.LoadSummary(ctx, id)
		s := summary.Content
		if s == "" {
			s = "[no summary]"
		}
		items = append(items, sessionItem{id: id, summary: s})
	}

	for i, it := range items {
		summary := it.summary
		if len(summary) > 50 {
			summary = summary[:47] + "..."
		}
		fmt.Fprintf(c.Out, "%d. %s  %s\n", i+1, it.id, summary)
	}

	if io == nil {
		fmt.Fprintln(c.Out, "(terminal required for interactive selection)")
		return nil
	}

	line, err := io.Prompt("enter number to resume (or Ctrl-C to cancel): ")
	if err != nil {
		if errors.Is(err, liner.ErrPromptAborted) {
			fmt.Fprintln(c.Out)
			return nil
		}
		return fmt.Errorf("read selection: %w", err)
	}
	return c.resumeSession(items, line)
}

func (c *CLI) resumeSession(items []sessionItem, line string) error {
	ctx := context.Background()
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var choice int
	if _, parseErr := fmt.Sscanf(line, "%d", &choice); parseErr != nil || choice < 1 || choice > len(items) {
		fmt.Fprintf(c.Out, "invalid choice: %q\n", line)
		return nil
	}

	id := items[choice-1].id
	fmt.Fprintf(c.Out, "resuming %s...\n", id)
	return c.Agent.Resume(ctx, id)
}

// replCmd drives the read-eval-print loop. When stdin is a
// terminal, it uses liner for history, line-editing and
// tab-completion; otherwise it falls back to bufio.Scanner
// (pipe/redirect/test fakes).
func (c *CLI) replCmd() error {
	if c.Agent == nil {
		return errors.New("repl: no agent available (config not loaded)")
	}
	if l, ok := tryNewLiner(c.In); ok {
		defer l.Close()
		return c.replLiner(l)
	}
	return c.replScanner()
}

// replLiner runs the REPL with liner (interactive terminal).
// liner provides history, Ctrl-A/E/B/F, and tab-completion.
func (c *CLI) replLiner(l *liner.State) error {
	fmt.Fprintln(c.Out, "chaosbot REPL — type '/help' for commands, Ctrl-D to exit")
	l.SetCtrlCAborts(true)
	l.SetCompleter(replComplete)
	for {
		line, err := l.Prompt("> ")
		if err != nil {
			if errors.Is(err, liner.ErrPromptAborted) {
				continue
			}
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		l.AppendHistory(line)

		if quit := c.replDispatch(line, l); quit {
			return nil
		}
	}
}

// replScanner runs the REPL with bufio.Scanner (non-interactive
// stdin: pipes, redirection, test fakes).
func (c *CLI) replScanner() error {
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
		if quit := c.replDispatch(line, nil); quit {
			return nil
		}
	}
}

// replDispatch handles one REPL line. Returns true if the REPL
// should exit. io is used for interactive prompts (e.g. /sessions
// number selection); pass nil in non-interactive mode.
func (c *CLI) replDispatch(line string, io REPLIO) bool {
	switch line {
	case "/reset":
		c.Agent.Reset()
		fmt.Fprintln(c.Out, "history cleared")
		return false
	case "/exit", "/quit":
		return true
	case "/help":
		fmt.Fprintln(c.Out, "commands:")
		fmt.Fprintln(c.Out, "  /reset     clear conversation history")
		fmt.Fprintln(c.Out, "  /sessions  list and resume saved sessions")
		fmt.Fprintln(c.Out, "  /exit      leave the REPL (alias: /quit)")
		fmt.Fprintln(c.Out, "  /help      show this message")
		fmt.Fprintln(c.Out, "  /tools     list registered tools")
		return false
	case "/tools":
		if c.Registry == nil {
			fmt.Fprintln(c.Out, "(no tools registered)")
			return false
		}
		for _, name := range c.Registry.Names() {
			fmt.Fprintln(c.Out, name)
		}
		return false
	case "/sessions":
		c.sessionsCmd(io)
		return false
	}
	reply, err := c.Agent.Run(context.Background(), line)
	if err != nil {
		fmt.Fprintln(c.ErrOut, "error:", agent.HumanError(err))
		return false
	}
	fmt.Fprintln(c.Out, stripThink(reply))
	return false
}

// replComplete returns tab completions for the current line.
// Matches slash commands when the line starts with "/".
func replComplete(line string) []string {
	if !strings.HasPrefix(line, "/") {
		return nil
	}
	cmds := []string{"/reset", "/sessions", "/exit", "/quit", "/help", "/tools"}
	var matches []string
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, line) {
			matches = append(matches, cmd)
		}
	}
	return matches
}

// tryNewLiner returns a *liner.State if r is a terminal and
// the platform supports interactive input, or nil/false.
func tryNewLiner(r io.Reader) (*liner.State, bool) {
	f, ok := r.(*os.File)
	if !ok {
		return nil, false
	}
	if !isTerminal(f.Fd()) {
		return nil, false
	}
	return liner.NewLiner(), true
}

// isTerminal returns whether the given file descriptor is a
// terminal.
func isTerminal(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

// maskKey redacts the middle of an API key for display: the
// first 4 and last 4 chars stay, the rest becomes "...".
func maskKey(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// stripThink removes reasoning content from model output. Defined
// in the agent package as agent.StripThink; this alias keeps the
// existing call sites in the CLI readable.
func stripThink(s string) string { return agent.StripThink(s) }
