package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/sausheong/ag/config"
	agtool "github.com/sausheong/ag/tool"
	"github.com/sausheong/ag/ui"
	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/providers/anthropic"
	"github.com/sausheong/harness/providers/openai"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	harnesstool "github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tools/mcp"
	"github.com/sausheong/harness/tool/skills"
)

const systemPrompt = `You are ag — a self-evolving, configurable agent. The user specifies tools and skills in ag.yaml, and you update yourself along the way, learning from every interaction.

## Self-learning (mandatory)

After EVERY successful CLI tool call, you MUST:
1. Review what you learned from the output — new flags, subcommands, error patterns, or usage that isn't in the current tool spec.
2. If you learned anything new, immediately call config_manage with action=update_tool to update the description, parameters, or examples.
3. If the output reveals a reusable pattern or workflow, call skill_manage to create or update a context skill.

Do not skip this step. Self-improvement after each tool use is your primary responsibility.

## Skills

Use skill_manage to create, update, and recall procedural skills.
After 2+ near-identical user requests in a session, save the pattern as a named macro skill.
When the user corrects your behaviour ("always do X"), save it as a context skill.

## Config

Use config_manage to read/write ag.yaml: update_tool, update_skill, set.

Always be terse and direct. Prefer tool use over explanation.`

// Build constructs a harness Runtime from the loaded config.
// Returns the runtime, the skill store (for system prompt injection), and any error.
//
// Resolution order (ag.yaml wins, .env/.environment is fallback):
//   - model:      ag.yaml > ANTHROPIC_MODEL env var
//   - auth_token: ag.yaml > ANTHROPIC_AUTH_TOKEN > ANTHROPIC_API_KEY env var
//   - base_url:   ag.yaml > ANTHROPIC_BASE_URL env var
func Build(cfg *config.Config, configPath string) (*runtime.Runtime, *agtool.JSONSkillStore, error) {
	model := cfg.Model
	if model == "" {
		model = os.Getenv("ANTHROPIC_MODEL")
	}

	// Determine provider from model prefix (e.g. "openai/gpt-4o" → openai).
	isOpenAI := strings.HasPrefix(model, "openai/")

	apiKey := cfg.AuthToken
	if apiKey == "" {
		if isOpenAI {
			apiKey = os.Getenv("OPENAI_API_KEY")
		} else {
			apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
			if apiKey == "" {
				apiKey = os.Getenv("ANTHROPIC_API_KEY")
			}
		}
	}
	if apiKey == "" {
		if isOpenAI {
			return nil, nil, fmt.Errorf("auth_token not set in ag.yaml and OPENAI_API_KEY not set in environment")
		}
		return nil, nil, fmt.Errorf("auth_token not set in ag.yaml and ANTHROPIC_AUTH_TOKEN/ANTHROPIC_API_KEY not set in environment")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		if isOpenAI {
			baseURL = os.Getenv("OPENAI_BASE_URL")
		} else {
			baseURL = os.Getenv("ANTHROPIC_BASE_URL")
		}
	}

	reg := harnesstool.NewRegistry()

	// Register config_manage tool
	reg.Register(&agtool.ConfigManageTool{ConfigPath: configPath})

	// Register cli_run_<name> tools for every bootstrapped tool spec (has a bin).
	for _, spec := range cfg.Tools {
		if spec.Bin != "" {
			reg.Register(&agtool.CLITool{Spec: spec})
		}
	}

	// Register web_<name>_<action> tools for every web tool spec (has a url).
	for _, spec := range cfg.Tools {
		if spec.IsWeb() {
			for _, wt := range agtool.WebToolsFromSpec(spec) {
				reg.Register(wt)
			}
		}
	}

	// Skill store backed by ag.yaml
	store := &agtool.JSONSkillStore{ConfigPath: configPath}

	// Register skill_manage tool
	reg.Register(&skills.SkillTool{Store: store})

	// Convert config MCP servers to harness ServerConfigs.
	var mcpServers []mcp.ServerConfig
	for _, s := range cfg.MCPServers {
		mcpServers = append(mcpServers, mcp.ServerConfig{
			Name:    s.Name,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			URL:     s.URL,
			Headers: s.Headers,
		})
	}

	var prov llm.LLMProvider
	if isOpenAI {
		prov = openai.NewOpenAIProviderWithKind(apiKey, baseURL, "openai-compatible")
	} else {
		prov = anthropic.NewAnthropicProvider(apiKey, baseURL)
	}
	sess := session.NewSession("ag", "main")

	// selfLearner tracks which CLI tools were called since the last user message
	// so we can append a targeted self-learning reminder to the next prompt.
	var mu sync.Mutex
	var usedTools []string

	hooks := runtime.LifecycleHooks{
		AfterToolUse: func(_ context.Context, toolName string, _ json.RawMessage, result harnesstool.ToolResult) {
			if !strings.HasPrefix(toolName, "cli_run_") && !strings.HasPrefix(toolName, "web_") {
				return
			}
			if result.Output != "" && result.Output != "(no output)" {
				mu.Lock()
				// Deduplicate — only track each tool name once per turn.
				for _, t := range usedTools {
					if t == toolName {
						mu.Unlock()
						return
					}
				}
				usedTools = append(usedTools, toolName)
				mu.Unlock()
			}
		},
		OnUserPromptSubmit: func(_ context.Context, prompt string, images []llm.ImageContent) (string, []llm.ImageContent, error) {
			mu.Lock()
			tools := usedTools
			usedTools = nil // reset for this turn
			mu.Unlock()
			if len(tools) == 0 {
				return prompt, images, nil
			}
			reminder := "\n\n[Self-learning reminder: you previously used " +
				strings.Join(tools, ", ") +
				". Before responding, check whether their output revealed new flags, " +
				"subcommands, or usage patterns. If so, update the tool spec and/or skills " +
				"via config_manage / skill_manage before answering.]"
			return prompt + reminder, images, nil
		},
	}

	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{
			Skills: &agtool.SkillProviderAdapter{Store: store},
			AgentLoop: runtime.LoopConfig{
				MaxToolConcurrency: 4,
				MaxAgentDepth:      1,
				Hooks:              hooks,
			},
		},
		runtime.RuntimeInputs{
			Provider: prov,
			Tools:    reg,
			Session:  sess,
		},
		runtime.AgentSpec{
			ID:           "ag",
			Name:         "ag",
			Model:        model,
			SystemPrompt: systemPrompt,
			MaxTurns:     20,
			MCPServers:   mcpServers,
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("building runtime: %w", err)
	}
	return rt, store, nil
}

// RunOnce executes a single agent turn and streams output to stdout.
func RunOnce(ctx context.Context, rt *runtime.Runtime, prompt string) error {
	events, err := rt.Run(ctx, prompt, nil)
	if err != nil {
		return err
	}
	return streamEvents(events)
}

// RunREPL runs an interactive REPL until EOF, blank line, or /exit.
// It watches configPath for changes after each turn; if ag.yaml is modified
// (e.g. by config_manage), it rebuilds the runtime so new tools and skills
// are available immediately in the same session.
func RunREPL(ctx context.Context, rt *runtime.Runtime, configPath string) {
	in := bufio.NewScanner(os.Stdin)
	fmt.Println(ui.Welcome())
	for {
		fmt.Print("\n" + ui.Prompt())
		if !in.Scan() || in.Text() == "" {
			return
		}
		switch in.Text() {
		case "/exit":
			return
		case "/help":
			fmt.Println(ui.Help())
			continue
		case "/config":
			if cfg, err := config.Load(configPath); err == nil {
				fmt.Println(ui.ConfigInfo(cfg.Model, cfg.AuthToken, cfg.BaseURL, configPath))
			} else {
				fmt.Fprintln(os.Stderr, ui.Error("reading config: "+err.Error()))
			}
			continue
		case "/tools":
			if cfg, err := config.Load(configPath); err == nil {
				rows := make([]struct{ Name, Bin, URL, Desc string }, len(cfg.Tools))
				for i, t := range cfg.Tools {
					rows[i] = struct{ Name, Bin, URL, Desc string }{t.Name, t.Bin, t.URL, t.Description}
				}
				fmt.Print(ui.ToolsList(rows))
			} else {
				fmt.Fprintln(os.Stderr, ui.Error("reading config: "+err.Error()))
			}
			continue
		case "/skills":
			if cfg, err := config.Load(configPath); err == nil {
				rows := make([]ui.SkillRow, len(cfg.Skills))
				for i, s := range cfg.Skills {
					rows[i] = ui.SkillRow{Name: s.Name, Type: s.Type, Description: s.Description}
				}
				fmt.Print(ui.SkillsTable(rows))
			} else {
				fmt.Fprintln(os.Stderr, ui.Error("reading config: "+err.Error()))
			}
			continue
		case "/mcp":
			if cfg, err := config.Load(configPath); err == nil {
				rows := make([]struct{ Name, Transport string }, len(cfg.MCPServers))
				for i, s := range cfg.MCPServers {
					t := s.URL
					if s.Command != "" {
						t = s.Command
						if len(s.Args) > 0 {
							t += " " + strings.Join(s.Args, " ")
						}
					}
					rows[i] = struct{ Name, Transport string }{s.Name, t}
				}
				fmt.Print(ui.MCPList(rows))
			} else {
				fmt.Fprintln(os.Stderr, ui.Error("reading config: "+err.Error()))
			}
			continue
		}

		// Snapshot mtime before the turn so we can detect writes by config_manage.
		mtimeBefore := configMtime(configPath)

		events, err := rt.Run(ctx, in.Text(), nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error(err.Error()))
			continue
		}
		if err := streamEvents(events); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error("stream: "+err.Error()))
		}

		// If ag.yaml was modified during the turn, reload and rebuild the runtime.
		if configMtime(configPath) != mtimeBefore {
			newCfg, err := config.Load(configPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error("reloading "+configPath+": "+err.Error()))
				continue
			}
			newRt, _, err := Build(newCfg, configPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error("rebuilding agent after "+configPath+" change: "+err.Error()))
				continue
			}
			rt.Close()
			rt = newRt
			fmt.Println(ui.BootstrapHeader(configPath + " updated — agent reloaded with new capabilities."))
		}
	}
}

// configMtime returns the modification time of the config file as a Unix nanosecond
// timestamp, or 0 if the file cannot be stat'd.
func configMtime(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

func streamEvents(events <-chan runtime.AgentEvent) error {
	fmt.Println()
	renderer := &ui.LineRenderer{}
	for ev := range events {
		switch ev.Type {
		case runtime.EventTextDelta:
			renderer.Feed(ev.Text)
		case runtime.EventToolCallStart:
			renderer.Flush()
			if ev.ToolCall != nil {
				fmt.Print(ui.ToolCall(ev.ToolCall.Name))
			}
		case runtime.EventToolResult:
			fmt.Print(ui.ToolOK())
		case runtime.EventError:
			renderer.Flush()
			return ev.Error
		case runtime.EventDone:
			renderer.Flush()
			if ev.Usage != nil {
				fmt.Println(ui.TokenUsage(ev.Usage.InputTokens, ev.Usage.OutputTokens))
			}
		}
	}
	return nil
}
