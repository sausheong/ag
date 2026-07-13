package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/sausheong/ag/agent"
	"github.com/sausheong/ag/bootstrap"
	"github.com/sausheong/ag/config"
	agtool "github.com/sausheong/ag/tool"
	"github.com/sausheong/ag/ui"
)

const configFile = "ag.yaml"

// activeCfgFile is the resolved config path for the current invocation.
// Set in main() after --config flag parsing; used by add subcommands.
var activeCfgFile = configFile

// loadDotEnv reads a .env file and sets any key=value pairs as environment
// variables, skipping lines that are blank or start with #. Existing env vars
// are not overwritten (consistent with how tools like direnv work).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // .env is optional
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

const skeletonYAML = `model: anthropic/claude-sonnet-4-5
auth_token:          # or set ANTHROPIC_AUTH_TOKEN in environment
base_url:            # optional: litellm or custom proxy endpoint
tools:
  - name: mytool
    bin: /path/to/mytool
    hint: brief description to guide bootstrap
skills: []
`

func main() {
	// Suppress harness provider INFO logs (stream usage counters, etc.) — only
	// show warnings and errors. Users see token counts via the styled EventDone footer.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))

	loadDotEnv(".env")

	args := os.Args[1:]

	// --config / -c overrides the config file path. Strip it from args before routing.
	cfgFile := configFile
	for i := 0; i < len(args); i++ {
		if (args[i] == "--config" || args[i] == "-c") && i+1 < len(args) {
			cfgFile = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
		if strings.HasPrefix(args[i], "--config=") {
			cfgFile = strings.TrimPrefix(args[i], "--config=")
			args = append(args[:i], args[i+1:]...)
			break
		}
	}

	activeCfgFile = cfgFile
	configOverridden := cfgFile != configFile

	// ag help — only when explicitly requested or invoked with no args and no config override.
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		printHelp(cfgFile)
		return
	}
	if len(args) == 0 && !configOverridden {
		printHelp(cfgFile)
		return
	}

	// "ag web login <name>" opens a browser with the persistent profile for manual login.
	if len(args) >= 2 && args[0] == "web" && args[1] == "login" {
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, ui.FatalError("usage: ag web login <name>"))
			os.Exit(1)
		}
		runWebLogin(args[2], cfgFile)
		return
	}

	// "ag add tool/skill/mcp ..." writes to ag.yaml directly, no agent needed.
	if len(args) >= 2 && args[0] == "add" {
		switch args[1] {
		case "tool":
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, ui.FatalError("usage: ag add tool <name> <bin> [hint]"))
				os.Exit(1)
			}
			runAddTool(args[2:])
			return
		case "skill":
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, ui.FatalError("usage: ag add skill <name> <body> [context|macro]"))
				os.Exit(1)
			}
			runAddSkill(args[2:])
			return
		case "mcp":
			runAddMCP(args[2:])
			return
		case "web":
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, ui.FatalError("usage: ag add web <name> <url> [hint]"))
				os.Exit(1)
			}
			runAddWeb(args[2:])
			return
		}
	}

	// Handle bootstrap before loading config — ag.yaml may not exist yet.
	if len(args) > 0 && args[0] == "bootstrap" {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			if !errors.Is(err, config.ErrNotFound) {
				fmt.Fprintln(os.Stderr, ui.FatalError("loading "+cfgFile+": "+err.Error()))
				os.Exit(1)
			}
			// Create skeleton and tell the user to fill it in.
			if err := os.WriteFile(cfgFile, []byte(skeletonYAML), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, ui.FatalError("creating "+cfgFile+": "+err.Error()))
				os.Exit(1)
			}
			fmt.Println(ui.BootstrapHeader("Created " + cfgFile + " — edit the tools list and run 'ag bootstrap' again."))
			fmt.Println(ui.BootstrapHeader("\nSkeleton written to " + cfgFile + ":"))
			fmt.Print(skeletonYAML)
			return
		}
		runBootstrapWithPath(cfg, cfgFile)
		return
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			fmt.Fprintln(os.Stderr, ui.FatalError(err.Error()))
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, ui.FatalError("loading "+cfgFile+": "+err.Error()))
		os.Exit(1)
	}

	// Handle skills subcommand.
	if len(args) > 0 && args[0] == "skills" {
		runSkills(cfg)
		return
	}

	// Auto-bootstrap tools that have a bin but no description yet.
	unbootstrapped := 0
	for _, t := range cfg.Tools {
		if (t.Bin != "" || t.IsWeb()) && !t.Bootstrapped() {
			unbootstrapped++
		}
	}
	if unbootstrapped > 0 {
		fmt.Println(ui.BootstrapHeader(fmt.Sprintf("%d tool(s) not yet bootstrapped — running bootstrap...", unbootstrapped)))
		runBootstrapWithPath(cfg, cfgFile)
		cfg, err = config.Load(cfgFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.FatalError("reloading config after bootstrap: "+err.Error()))
			os.Exit(1)
		}
	}

	rt, _, err := agent.Build(cfg, cfgFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.FatalError("starting agent: "+err.Error()))
		os.Exit(1)
	}
	defer rt.Close()

	ctx := context.Background()

	if len(args) > 0 {
		// One-shot mode: ag "do something"
		prompt := strings.Join(args, " ")
		if err := agent.RunOnce(ctx, rt, prompt); err != nil {
			fmt.Fprintln(os.Stderr, ui.FatalError(err.Error()))
			os.Exit(1)
		}
		return
	}

	// REPL mode
	agent.RunREPL(ctx, rt, cfgFile)
}

func printHelp(cfgFile string) {
	fmt.Println(ui.BootstrapHeader("ag") + " — self-evolving configurable agent")
	fmt.Println()
	fmt.Println(ui.BootstrapHeader("Usage:"))
	rows := [][]string{
		{"ag", "Interactive REPL"},
		{"ag \"request\"", "One-shot — run one request and exit"},
		{"ag bootstrap", "Create ag.yaml skeleton or enrich tool specs"},
		{"ag skills", "List learned skills"},
		{"ag help", "Show this help"},
		{"ag add tool <name> <bin> [hint]", "Add a CLI tool and bootstrap it"},
		{"ag add skill <name> <body> [context|macro]", "Add or update a skill"},
		{"ag add mcp <name> --command <cmd>", "Add a stdio MCP server"},
		{"ag add mcp <name> --url <url>", "Add an HTTP MCP server"},
		{"ag add web <name> <url> [hint]", "Add a web app tool and bootstrap it"},
		{"ag web login <name>", "Open browser to log into a web tool (saves session)"},
	}
	col0 := 0
	for _, r := range rows {
		if len(r[0]) > col0 {
			col0 = len(r[0])
		}
	}
	for _, r := range rows {
		fmt.Printf("  %-*s  %s\n", col0, r[0], r[1])
	}
	fmt.Println()
	fmt.Println(ui.BootstrapHeader("Flags:"))
	fmt.Printf("  %-*s  %s\n", col0, "--config <path>, -c <path>", "Use a different config file (default: "+cfgFile+")")
	fmt.Println()
	fmt.Println(ui.BootstrapHeader("REPL commands:"))
	fmt.Printf("  %-*s  %s\n", col0, "/help", "Show REPL commands")
	fmt.Printf("  %-*s  %s\n", col0, "/exit", "Exit the REPL")
}

func runBootstrap(cfg *config.Config) {
	runBootstrapWithPath(cfg, activeCfgFile)
}

func runBootstrapWithPath(cfg *config.Config, cfgFile string) {
	// Bootstrap tools that have a bin path or a url declared.
	var toBoot []config.ToolSpec
	for _, t := range cfg.Tools {
		if t.Bin != "" || t.IsWeb() {
			toBoot = append(toBoot, t)
		}
	}
	if len(toBoot) == 0 {
		fmt.Println(ui.BootstrapHeader("No tools with a bin or url declared in " + cfgFile + " — add entries to the 'tools' list."))
		return
	}

	// Split into CLI tools (have bin) and web tools (have url).
	var cliTools, webTools []config.ToolSpec
	for _, t := range toBoot {
		if t.IsWeb() {
			webTools = append(webTools, t)
		} else {
			cliTools = append(cliTools, t)
		}
	}

	rt, _, err := agent.Build(cfg, cfgFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.FatalError("starting agent for bootstrap: "+err.Error()))
		os.Exit(1)
	}
	defer rt.Close()

	ctx := context.Background()

	// Bootstrap CLI tools.
	if len(cliTools) > 0 {
		fmt.Println(ui.BootstrapHeader("Extracting help from CLI tools..."))
		helpTexts := make(map[string]string, len(cliTools))
		for _, tool := range cliTools {
			fmt.Print(ui.BootstrapStep(tool.Name))
			help, err := bootstrap.ExtractHelp(tool)
			if err != nil {
				fmt.Println(ui.BootstrapWarning(err.Error()))
				continue
			}
			helpTexts[tool.Name] = help
			fmt.Println(ui.BootstrapOK())
		}
		prompt := bootstrap.BuildPrompt(cliTools, helpTexts)
		fmt.Println(ui.BootstrapHeader("Generating CLI tool specs..."))
		if err := agent.RunOnce(ctx, rt, prompt); err != nil {
			fmt.Fprintln(os.Stderr, ui.FatalError("bootstrap error: "+err.Error()))
			os.Exit(1)
		}
	}

	// Bootstrap web tools.
	if len(webTools) > 0 {
		fmt.Println(ui.BootstrapHeader("Bootstrapping web tools (opening browser)..."))
		for _, wt := range webTools {
			fmt.Print(ui.BootstrapStep(wt.Name + " (web)"))
			pageText, err := bootstrap.ExtractWebContext(wt)
			if err != nil {
				fmt.Println(ui.BootstrapWarning(err.Error()))
				continue
			}
			fmt.Println(ui.BootstrapOK())
			prompt := bootstrap.BuildWebPrompt(wt, pageText)
			fmt.Println(ui.BootstrapHeader("Generating web action specs for " + wt.Name + "..."))
			if err := agent.RunOnce(ctx, rt, prompt); err != nil {
				fmt.Fprintln(os.Stderr, ui.FatalError("web bootstrap error: "+err.Error()))
			}
		}
	}

	fmt.Println("\n" + ui.BootstrapHeader("Bootstrap complete. "+cfgFile+" updated."))
}

// runAddTool adds a tool entry to ag.yaml and triggers bootstrap.
// Usage: ag add tool <name> <bin> [hint]
func runAddTool(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, ui.FatalError("usage: ag add tool <name> <bin> [hint]"))
		os.Exit(1)
	}
	name, bin := args[0], args[1]
	hint := ""
	if len(args) >= 3 {
		hint = strings.Join(args[2:], " ")
	}

	cfg, err := config.Load(activeCfgFile)
	if err != nil {
		if !errors.Is(err, config.ErrNotFound) {
			fmt.Fprintln(os.Stderr, ui.FatalError("loading ag.yaml: "+err.Error()))
			os.Exit(1)
		}
		cfg = &config.Config{Tools: []config.ToolSpec{}, Skills: []config.SkillEntry{}}
	}

	// Reject duplicates.
	for _, t := range cfg.Tools {
		if t.Name == name {
			fmt.Fprintln(os.Stderr, ui.FatalError(fmt.Sprintf("tool %q already exists — edit ag.yaml to update it", name)))
			os.Exit(1)
		}
	}

	cfg.Tools = append(cfg.Tools, config.ToolSpec{Name: name, Bin: bin, Hint: hint})
	if err := config.Save(activeCfgFile, cfg); err != nil {
		fmt.Fprintln(os.Stderr, ui.FatalError("saving ag.yaml: "+err.Error()))
		os.Exit(1)
	}
	fmt.Println(ui.BootstrapHeader(fmt.Sprintf("Added tool %q (%s) to ag.yaml.", name, bin)))

	// Run bootstrap to enrich the new tool spec.
	fmt.Println(ui.BootstrapHeader("Running bootstrap to generate tool spec..."))
	runBootstrap(cfg)
}

// runAddSkill adds a skill entry to ag.yaml directly.
// Usage: ag add skill <name> <body> [context|macro]
func runAddSkill(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, ui.FatalError("usage: ag add skill <name> <body> [context|macro]"))
		os.Exit(1)
	}
	name := args[0]
	body := args[1]
	skillType := "context"
	if len(args) >= 3 && (args[2] == "macro" || args[2] == "context") {
		skillType = args[2]
	}

	cfg, err := config.Load(activeCfgFile)
	if err != nil {
		if !errors.Is(err, config.ErrNotFound) {
			fmt.Fprintln(os.Stderr, ui.FatalError("loading ag.yaml: "+err.Error()))
			os.Exit(1)
		}
		cfg = &config.Config{Tools: []config.ToolSpec{}, Skills: []config.SkillEntry{}}
	}

	now := time.Now().UTC()
	// Update if exists, append if new.
	updated := false
	for i, sk := range cfg.Skills {
		if sk.Name == name {
			cfg.Skills[i].Body = body
			cfg.Skills[i].Type = skillType
			cfg.Skills[i].UpdatedAt = now
			updated = true
			break
		}
	}
	if !updated {
		cfg.Skills = append(cfg.Skills, config.SkillEntry{
			Name:      name,
			Body:      body,
			Type:      skillType,
			Origin:    "user",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	if err := config.Save(activeCfgFile, cfg); err != nil {
		fmt.Fprintln(os.Stderr, ui.FatalError("saving ag.yaml: "+err.Error()))
		os.Exit(1)
	}
	action := "Added"
	if updated {
		action = "Updated"
	}
	fmt.Println(ui.BootstrapHeader(fmt.Sprintf("%s skill %q (%s) in ag.yaml.", action, name, skillType)))
}

// runAddMCP adds an MCP server entry to ag.yaml.
//
// Stdio:  ag add mcp <name> --command <cmd> [--args arg1,arg2] [--env K=V,K=V]
// HTTP:   ag add mcp <name> --url <url> [--header K=V,K=V]
func runAddMCP(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, ui.FatalError("usage: ag add mcp <name> --command <cmd> [--args ...] [--env K=V] OR --url <url> [--header K=V]"))
		os.Exit(1)
	}
	name := args[0]
	rest := args[1:]

	// Parse simple flag-style args manually (no external dep).
	var command, mcpURL string
	var mcpArgs []string
	env := map[string]string{}
	headers := map[string]string{}

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--command", "-c":
			i++
			if i < len(rest) {
				command = rest[i]
			}
		case "--url", "-u":
			i++
			if i < len(rest) {
				mcpURL = rest[i]
			}
		case "--args", "-a":
			i++
			if i < len(rest) {
				mcpArgs = strings.Split(rest[i], ",")
			}
		case "--env", "-e":
			i++
			if i < len(rest) {
				for _, kv := range strings.Split(rest[i], ",") {
					k, v, _ := strings.Cut(kv, "=")
					if k != "" {
						env[k] = v
					}
				}
			}
		case "--header", "-H":
			i++
			if i < len(rest) {
				for _, kv := range strings.Split(rest[i], ",") {
					k, v, _ := strings.Cut(kv, "=")
					if k != "" {
						headers[k] = v
					}
				}
			}
		}
	}

	if command == "" && mcpURL == "" {
		fmt.Fprintln(os.Stderr, ui.FatalError("ag add mcp requires --command <cmd> (stdio) or --url <url> (HTTP)"))
		os.Exit(1)
	}

	cfg, err := config.Load(activeCfgFile)
	if err != nil {
		if !errors.Is(err, config.ErrNotFound) {
			fmt.Fprintln(os.Stderr, ui.FatalError("loading ag.yaml: "+err.Error()))
			os.Exit(1)
		}
		cfg = &config.Config{Tools: []config.ToolSpec{}, Skills: []config.SkillEntry{}}
	}

	// Reject duplicates.
	for _, s := range cfg.MCPServers {
		if s.Name == name {
			fmt.Fprintln(os.Stderr, ui.FatalError(fmt.Sprintf("MCP server %q already exists — edit ag.yaml to update it", name)))
			os.Exit(1)
		}
	}

	entry := config.MCPServerConfig{Name: name}
	if command != "" {
		entry.Command = command
		if len(mcpArgs) > 0 {
			entry.Args = mcpArgs
		}
		if len(env) > 0 {
			entry.Env = env
		}
	} else {
		entry.URL = mcpURL
		if len(headers) > 0 {
			entry.Headers = headers
		}
	}

	cfg.MCPServers = append(cfg.MCPServers, entry)
	if err := config.Save(activeCfgFile, cfg); err != nil {
		fmt.Fprintln(os.Stderr, ui.FatalError("saving ag.yaml: "+err.Error()))
		os.Exit(1)
	}

	transport := command
	if mcpURL != "" {
		transport = mcpURL
	}
	fmt.Println(ui.BootstrapHeader(fmt.Sprintf("Added MCP server %q (%s) to ag.yaml.", name, transport)))
	fmt.Println(ui.BootstrapHeader("Run 'ag' to start using it — MCP tools are loaded automatically at startup."))
}

// runWebLogin opens a persistent Chrome profile at the tool's URL so the user
// can log in manually. The browser stays open until the user presses Enter.
// Usage: ag web login <name>
func runWebLogin(name, cfgFile string) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.FatalError("loading config: "+err.Error()))
		os.Exit(1)
	}

	var spec *config.ToolSpec
	for i, t := range cfg.Tools {
		if t.Name == name && t.IsWeb() {
			spec = &cfg.Tools[i]
			break
		}
	}
	if spec == nil {
		fmt.Fprintln(os.Stderr, ui.FatalError(fmt.Sprintf("no web tool %q found in %s", name, cfgFile)))
		os.Exit(1)
	}

	profileDir, err := agtool.ProfileDir(spec.Profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.FatalError("profile error: "+err.Error()))
		os.Exit(1)
	}

	fmt.Println(ui.BootstrapHeader(fmt.Sprintf("Opening %s in browser (profile: %s)...", spec.URL, profileDir)))
	fmt.Println(ui.BootstrapHeader("Log in, then press Enter here to save the session and close the browser."))

	// Use Playwright's own persistent context for login — the same profile directory
	// that Execute() uses. Cookies are written directly to the profile; no export needed.
	if err := agtool.LoginWithPlaywright(profileDir, spec.URL); err != nil {
		fmt.Fprintln(os.Stderr, ui.FatalError("login error: "+err.Error()))
		os.Exit(1)
	}

	fmt.Println(ui.BootstrapHeader("Session saved. Future ag tool calls for " + name + " will reuse this login."))
}

// runAddWeb adds a web tool entry to ag.yaml and triggers bootstrap.
// Usage: ag add web <name> <url> [hint]
func runAddWeb(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, ui.FatalError("usage: ag add web <name> <url> [hint]"))
		os.Exit(1)
	}
	name, rawURL := args[0], args[1]
	hint := ""
	if len(args) >= 3 {
		hint = strings.Join(args[2:], " ")
	}

	cfg, err := config.Load(activeCfgFile)
	if err != nil {
		if !errors.Is(err, config.ErrNotFound) {
			fmt.Fprintln(os.Stderr, ui.FatalError("loading ag.yaml: "+err.Error()))
			os.Exit(1)
		}
		cfg = &config.Config{Tools: []config.ToolSpec{}, Skills: []config.SkillEntry{}}
	}

	for _, t := range cfg.Tools {
		if t.Name == name {
			fmt.Fprintln(os.Stderr, ui.FatalError(fmt.Sprintf("tool %q already exists — edit ag.yaml to update it", name)))
			os.Exit(1)
		}
	}

	cfg.Tools = append(cfg.Tools, config.ToolSpec{Name: name, URL: rawURL, Hint: hint})
	if err := config.Save(activeCfgFile, cfg); err != nil {
		fmt.Fprintln(os.Stderr, ui.FatalError("saving ag.yaml: "+err.Error()))
		os.Exit(1)
	}
	fmt.Println(ui.BootstrapHeader(fmt.Sprintf("Added web tool %q (%s) to ag.yaml.", name, rawURL)))
	fmt.Println(ui.BootstrapHeader("Running bootstrap to generate action specs..."))
	runBootstrap(cfg)
}

func runSkills(cfg *config.Config) {
	rows := make([]ui.SkillRow, len(cfg.Skills))
	for i, sk := range cfg.Skills {
		rows[i] = ui.SkillRow{
			Name:        sk.Name,
			Type:        sk.Type,
			Description: sk.Description,
		}
	}
	fmt.Print(ui.SkillsTable(rows))
}
