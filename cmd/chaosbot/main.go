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
)

// version is set at build time via -ldflags "-X main.version=$(VERSION)".
const version = "dev"

func main() {
	configPath := flag.String("config", "", "path to config file (env-only when empty)")
	flag.Parse()
	args := flag.Args()

	cliApp := di.GetDI[*cli.CLI](buildContainer(*configPath))

	if err := cliApp.Run(args); err != nil {
		fmt.Fprintln(os.Stderr, "chaosbot:", err)
		os.Exit(1)
	}
}
