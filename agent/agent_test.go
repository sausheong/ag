package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sausheong/ag/agent"
	"github.com/sausheong/ag/config"
)

func TestBuildRequiresAPIKey(t *testing.T) {
	// Temporarily unset both key vars to test error path
	origKey := os.Getenv("ANTHROPIC_API_KEY")
	origToken := os.Getenv("ANTHROPIC_AUTH_TOKEN")
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_AUTH_TOKEN")
	defer func() {
		os.Setenv("ANTHROPIC_API_KEY", origKey)
		os.Setenv("ANTHROPIC_AUTH_TOKEN", origToken)
	}()

	cfg := &config.Config{
		Model: "anthropic/claude-haiku-4-5-20251001",
		Tools: []config.ToolSpec{},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ag.yaml")
	_ = config.Save(path, cfg)

	_, _, err := agent.Build(cfg, path)
	if err == nil {
		t.Error("expected error when ANTHROPIC_API_KEY is unset")
	}
}

func TestBuildWithTools(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	cfg := &config.Config{
		Model: "anthropic/claude-haiku-4-5-20251001",
		Tools: []config.ToolSpec{
			{Name: "echo", Bin: "echo", Description: "Echo text"},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ag.yaml")
	_ = config.Save(path, cfg)

	rt, store, err := agent.Build(cfg, path)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer rt.Close()
	if store == nil {
		t.Error("expected non-nil skill store")
	}
}
