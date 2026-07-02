package main

import (
	"testing"

	"github.com/hyperchao/di"

	"chaosbot/cmd/chaosbot/cli"
	"chaosbot/internal/config"
)

func TestVersion_NoAPIKey_Succeeds(t *testing.T) {
	t.Setenv("CHAOSBOT_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := testCLIFromContainer(t, cfg)
	if err := c.Run([]string{"version"}); err != nil {
		t.Fatalf("Run(version): %v", err)
	}
}

func TestConfig_NoAPIKey_Succeeds(t *testing.T) {
	t.Setenv("CHAOSBOT_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := testCLIFromContainer(t, cfg)
	if err := c.Run([]string{"config"}); err != nil {
		t.Fatalf("Run(config): %v", err)
	}
}

func testCLIFromContainer(t *testing.T, cfg *config.Config) *cli.CLI {
	t.Helper()
	c := buildContainer(cfg)
	return di.GetDI[*cli.CLI](c)
}
