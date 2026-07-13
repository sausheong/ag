package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sausheong/ag/config"
	"github.com/sausheong/ag/ui"
	harnesstool "github.com/sausheong/harness/tool"
)

// styledWriter captures subprocess output into buf and writes
// lipgloss-styled lines to stdout as each line arrives.
type styledWriter struct {
	buf  *bytes.Buffer
	line bytes.Buffer
}

func (w *styledWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	w.line.Write(p)
	// Flush complete lines to stdout with styling.
	for {
		s := w.line.String()
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			break
		}
		fmt.Print(ui.RenderToolOutput(s[:nl+1]))
		w.line.Reset()
		w.line.WriteString(s[nl+1:])
	}
	return len(p), nil
}

func (w *styledWriter) flush() {
	if s := w.line.String(); s != "" {
		fmt.Print(ui.RenderToolOutput(s))
		w.line.Reset()
	}
}

// CLITool wraps one declared tool spec as a harness tool.
type CLITool struct {
	Spec config.ToolSpec
}

func (t *CLITool) Name() string { return "cli_run_" + t.Spec.Name }

func (t *CLITool) Description() string { return t.Spec.Description }

func (t *CLITool) Parameters() json.RawMessage {
	if t.Spec.Parameters != nil {
		return t.Spec.Parameters
	}
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"args": {
				"type": "string",
				"description": "Command-line arguments to pass to the tool, as a single string."
			}
		}
	}`)
}

func (t *CLITool) IsConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *CLITool) Execute(ctx context.Context, raw json.RawMessage) (harnesstool.ToolResult, error) {
	// Parse input as a generic map so we handle both rich schemas (named
	// properties) and the legacy flat {"args": "..."} schema.
	var props map[string]json.RawMessage
	_ = json.Unmarshal(raw, &props)

	argv := append(t.Spec.PrefixArgs, t.buildArgv(props)...)

	cmd := exec.CommandContext(ctx, t.Spec.Bin, argv...)

	// styledWriter tees subprocess output: captures to buf for the ToolResult
	// and writes lipgloss-styled lines to stdout as they arrive.
	var buf bytes.Buffer
	sw := &styledWriter{buf: &buf}
	cmd.Stdout = sw
	cmd.Stderr = sw

	fmt.Println() // blank line before streamed output
	err := cmd.Run()
	sw.flush() // flush any partial last line
	output := buf.String()
	if err != nil {
		return harnesstool.ToolResult{
			Output: fmt.Sprintf("exit error: %v\n%s", err, output),
		}, nil
	}
	if output == "" {
		output = "(no output)"
	}
	return harnesstool.ToolResult{Output: output}, nil
}

// buildArgv constructs the argument slice from the LLM's named property map.
//
// Order:
//  1. Positional params (t.Spec.Positional), in declared order, as bare values.
//  2. Remaining non-positional params as --key value (booleans as bare --key).
//  3. Legacy fallback: if the input has only an "args" key, split it on whitespace.
func (t *CLITool) buildArgv(props map[string]json.RawMessage) []string {
	// Legacy flat args string
	if len(props) == 0 {
		return nil
	}
	if raw, ok := props["args"]; ok && len(props) == 1 {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return strings.Fields(s)
		}
	}

	positionalSet := make(map[string]bool, len(t.Spec.Positional))
	for _, p := range t.Spec.Positional {
		positionalSet[p] = true
	}

	var argv []string

	// 1. Positional args in declared order.
	for _, name := range t.Spec.Positional {
		raw, ok := props[name]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			argv = append(argv, s)
		}
	}

	// 2. Named flags for all non-positional properties.
	for name, raw := range props {
		if positionalSet[name] || name == "args" {
			continue
		}
		flag := "--" + strings.ReplaceAll(name, "_", "-")

		// Boolean: --flag (true) or omit (false)
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			if b {
				argv = append(argv, flag)
			}
			continue
		}

		// String / number: --flag value
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			argv = append(argv, flag, s)
			continue
		}
		// Number (int/float) — re-marshal as string
		var n json.Number
		if json.Unmarshal(raw, &n) == nil {
			argv = append(argv, flag, n.String())
		}
	}

	return argv
}

var _ harnesstool.Tool = (*CLITool)(nil)
