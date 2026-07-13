package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sausheong/ag/config"
	agtool "github.com/sausheong/ag/tool"
)

func TestCLIToolName(t *testing.T) {
	ct := &agtool.CLITool{
		Spec: config.ToolSpec{Name: "git", Bin: "git", Description: "Git VCS"},
	}
	if ct.Name() != "cli_run_git" {
		t.Errorf("Name: got %q want %q", ct.Name(), "cli_run_git")
	}
}

func TestCLIToolExecuteEcho(t *testing.T) {
	ct := &agtool.CLITool{
		Spec: config.ToolSpec{
			Name:        "echo",
			Bin:         "echo",
			Description: "Echo arguments",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"args":{"type":"string"}}}`),
		},
	}
	input, _ := json.Marshal(map[string]string{"args": "hello world"})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}

func TestCLIToolPositionalArgs(t *testing.T) {
	ct := &agtool.CLITool{
		Spec: config.ToolSpec{
			Name:        "echo",
			Bin:         "echo",
			Description: "Echo text",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"text":{"type":"string"},
					"n":{"type":"boolean"}
				}
			}`),
			Positional: []string{"text"},
		},
	}
	input, _ := json.Marshal(map[string]any{"text": "hello world", "n": false})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Output != "hello world\n" {
		t.Errorf("Output: got %q want %q", result.Output, "hello world\n")
	}
}

func TestCLIToolNamedFlagBool(t *testing.T) {
	ct := &agtool.CLITool{
		Spec: config.ToolSpec{
			Name:       "echo",
			Bin:        "echo",
			Positional: []string{"text"},
			Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"},"n":{"type":"boolean"}}}`),
		},
	}
	input, _ := json.Marshal(map[string]any{"text": "hi", "n": false})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Output != "hi\n" {
		t.Errorf("Output with n=false: got %q", result.Output)
	}
}

func TestCLIToolExecuteFailure(t *testing.T) {
	ct := &agtool.CLITool{
		Spec: config.ToolSpec{Name: "false", Bin: "false", Description: "Always fails"},
	}
	input, _ := json.Marshal(map[string]string{})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute returned Go error (should return tool error): %v", err)
	}
	_ = result.Output
}
