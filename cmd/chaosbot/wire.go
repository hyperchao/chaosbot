package main

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/hyperchao/di"

	"chaosbot/cmd/chaosbot/cli"
	"chaosbot/internal/agent"
	"chaosbot/internal/config"
	"chaosbot/internal/provider"
	"chaosbot/internal/provider/openai"
	"chaosbot/internal/tools/fs"
	"chaosbot/internal/tools/shell"
)

// buildContainer wires the di container with everything cli.CLI
// needs. cfg is the loaded config (or nil when --config was empty
// and env vars didn't yield one); the caller has already printed
// any Load error. Interface-typed factories must return a non-nil
// value; emptyProvider stands in for the openai-backed provider
// when the API key is missing.
func buildContainer(cfg *config.Config) *di.DI {
	c := di.New()

	// Terminal / I/O + version.
	di.RegisterAliasDI(c, "in", func() io.Reader { return os.Stdin })
	di.RegisterAliasDI(c, "out", func() io.Writer { return os.Stdout })
	di.RegisterAliasDI(c, "errout", func() io.Writer { return os.Stderr })
	di.RegisterAliasDI(c, "version", func() string { return version })

	// Config.
	di.RegisterDI(c, func() *config.Config { return cfg })

	// Provider.
	di.RegisterDI(c, func() provider.Provider {
		if cfg == nil || cfg.Provider.APIKey == "" {
			return emptyProvider{}
		}
		return openai.New(provider.Config{
			Name:    cfg.Provider.Name,
			APIKey:  cfg.Provider.APIKey,
			BaseURL: cfg.Provider.BaseURL,
			OrgID:   cfg.Provider.OrgID,
			Timeout: cfg.Provider.Timeout,
		})
	})

	// Agent. Cfg translates *config.Config into the agent's
	// own DTO; missing config falls back to the zero value.
	// The Registry is built with the default tool set here;
	// future phases add more tools (write_file, edit_file,
	// shell, web_fetch) to this same factory closure.
	di.RegisterDI(c, func() *agent.Registry {
		r := agent.NewRegistry()
		r.Register(&fs.ReadFileTool{})
		r.Register(&fs.WriteFileTool{})
		r.Register(&fs.EditFileTool{})
		r.Register(&shell.ShellTool{})
		return r
	})
	di.RegisterDI(c, func() agent.Config {
		if cfg == nil {
			return agent.Config{}
		}
		return agent.Config{
			System:      cfg.System,
			Model:       cfg.Provider.Model,
			Temperature: cfg.Temperature,
			MaxTokens:   cfg.MaxTokens,
			MaxSteps:    cfg.MaxSteps,
		}
	})
	di.RegisterDI(c, func() agent.Agent { return agent.New() })

	// CLI.
	di.RegisterDI(c, func() *cli.CLI { return &cli.CLI{} })

	return c
}

// emptyProvider is a no-op provider.Provider that returns a
// friendly error on Chat. It stands in for the openai-backed
// provider when the API key is missing; the error propagates
// through the agent loop's normal error path.
type emptyProvider struct{}

var errNoProvider = errors.New("no provider configured (set CHAOSBOT_API_KEY or pass --config)")

func (emptyProvider) Name() string { return "empty" }

func (emptyProvider) Chat(_ context.Context, _ provider.Request) (*provider.Response, error) {
	return nil, errNoProvider
}
