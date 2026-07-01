// Command chaosbot is a tool-using AI agent CLI.
//
// Composition root: parse flags, build the di container, get
// the CLI, dispatch. The actual wiring (provider, agent, cli)
// lives in wire.go.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/hyperchao/di"

	"chaosbot/cmd/chaosbot/cli"
	"chaosbot/internal/config"
)

// version is set at build time via -ldflags "-X main.version=$(VERSION)".
// Must be `var` (not `const`) for -X to take effect: consts are
// folded at the use site during compilation and have no addressable
// storage left for the linker to overwrite.
var version = "dev"

func main() {
	configPath := flag.String("config", "", "path to config file (env-only when empty)")
	logFile := flag.String("log-file", "", "write logs to this file in addition to stderr (empty = stderr only)")
	logLevel := flag.String("log-level", "error", "log level: none|debug|info|warn|error (default error keeps stderr quiet under the REPL)")
	flag.Parse()
	args := flag.Args()

	if *configPath != "" {
		if _, err := os.Stat(*configPath); err != nil {
			fmt.Fprintf(os.Stderr, "chaosbot: --config %s: %v\n", *configPath, err)
			os.Exit(1)
		}
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chaosbot:", err)
		os.Exit(1)
	}

	if err := setupLogging(*logFile, *logLevel); err != nil {
		fmt.Fprintln(os.Stderr, "chaosbot:", err)
		os.Exit(1)
	}

	cliApp := di.GetDI[*cli.CLI](buildContainer(cfg))

	if err := cliApp.Run(args); err != nil {
		fmt.Fprintln(os.Stderr, "chaosbot:", err)
		os.Exit(1)
	}
}
