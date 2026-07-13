package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/sausheong/ag/bootstrap"
	"github.com/sausheong/ag/config"
)

func TestExtractHelpEcho(t *testing.T) {
	tool := config.ToolSpec{Name: "echo", Bin: "echo"}
	out, err := bootstrap.ExtractHelp(tool)
	_ = err
	_ = out
}

func TestExtractHelpGit(t *testing.T) {
	tool := config.ToolSpec{Name: "git", Bin: "git"}
	out, err := bootstrap.ExtractHelp(tool)
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty help output")
	}
}

func TestBuildPrompt(t *testing.T) {
	tools := []config.ToolSpec{
		{Name: "git", Bin: "git", Hint: "focus on branch ops"},
		{Name: "jq", Bin: "jq"},
	}
	helpTexts := map[string]string{
		"git": "usage: git [--version] ...",
		"jq":  "jq - commandline JSON processor",
	}
	prompt := bootstrap.BuildPrompt(tools, helpTexts)
	if !strings.Contains(prompt, "git") {
		t.Error("prompt should mention git")
	}
	if !strings.Contains(prompt, "focus on branch ops") {
		t.Error("prompt should include hint")
	}
	if !strings.Contains(prompt, "config_manage") {
		t.Error("prompt should instruct use of config_manage")
	}
}
