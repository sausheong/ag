package bootstrap

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
	"github.com/sausheong/ag/config"
	"github.com/sausheong/ag/tool"
)

func chromeArgs() []string {
	if runtime.GOOS == "linux" {
		return []string{"--no-sandbox"}
	}
	return nil
}

// ExtractHelp runs the binary with several strategies to extract help text,
// trying each in order until one produces non-empty output:
//  1. `<bin>` (no args) — captures default/usage output
//  2. `<bin> --help`
//  3. `<bin> help`
//
// Exit code is ignored — many CLIs exit non-zero for help invocations.
// All unique results are concatenated so distinct sections from each
// strategy are preserved.
func ExtractHelp(tool config.ToolSpec) (string, error) {
	var parts []string
	seen := make(map[string]bool)

	for _, args := range [][]string{{}, {"--help"}, {"help"}} {
		out := runHelpArgs(tool.Bin, args...)
		trimmed := strings.TrimSpace(out)
		if trimmed != "" && !seen[trimmed] {
			seen[trimmed] = true
			parts = append(parts, out)
		}
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("could not extract help for %q", tool.Bin)
	}
	return strings.Join(parts, "\n---\n"), nil
}

// runHelpArgs executes bin with the given args and returns combined stdout+stderr.
// Exit code is ignored — many CLIs exit non-zero for help invocations.
func runHelpArgs(bin string, args ...string) string {
	cmd := exec.Command(bin, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	_ = cmd.Run()
	return buf.String()
}

// BuildPrompt formats the bootstrap prompt for the LLM.
// It instructs the agent to enrich each tool spec and call config_manage to save it,
// then generate starter context skills.
func BuildPrompt(tools []config.ToolSpec, helpTexts map[string]string) string {
	var b strings.Builder

	b.WriteString("You are bootstrapping an ag agent configuration. ")
	b.WriteString("For each CLI tool below, enrich its tool spec and call config_manage with action=update_tool to save it.\n\n")
	b.WriteString("For each tool spec, provide:\n")
	b.WriteString("- name: the tool name (keep as-is)\n")
	b.WriteString("- bin: the binary path (keep as-is)\n")
	b.WriteString("- description: what the tool does (1-2 sentences)\n")
	b.WriteString("- parameters: JSON Schema object with ALL flag/argument properties as named fields\n")
	b.WriteString("- positional: array of parameter names that are positional arguments (not --flags), in the order they appear on the command line. IMPORTANT: if the CLI takes a positional argument (e.g. a URL or path before any flags), list its parameter name here.\n")
	b.WriteString("- examples: 2-3 example invocations as plain strings\n\n")
	b.WriteString("After enriching all tool specs, generate 2-3 starter context skills ")
	b.WriteString("describing what each tool is useful for, and call config_manage with action=update_skill for each.\n\n")
	b.WriteString("--- TOOLS TO BOOTSTRAP ---\n\n")

	for _, tool := range tools {
		fmt.Fprintf(&b, "## %s (bin: %s)\n", tool.Name, tool.Bin)
		if tool.Hint != "" {
			fmt.Fprintf(&b, "Hint: %s\n", tool.Hint)
		}
		if help, ok := helpTexts[tool.Name]; ok && help != "" {
			const maxHelp = 3000
			if len(help) > maxHelp {
				help = help[:maxHelp] + "\n...(truncated)"
			}
			b.WriteString("Help output:\n```\n")
			b.WriteString(help)
			b.WriteString("\n```\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("Call config_manage update_tool for each tool (preserving bin and name), ")
	b.WriteString("then config_manage update_skill for starter context skills. ")
	b.WriteString("Respond with a brief summary of what you generated when done.")
	return b.String()
}

// ExtractWebContext opens the URL in a visible browser, waits for the page to
// stabilise, and returns the page's visible text content for use in the
// web bootstrap prompt. The browser is always visible during bootstrap so the
// user can log in if needed.
func ExtractWebContext(spec config.ToolSpec) (string, error) {
	profileDir, err := tool.ProfileDir(spec.Profile)
	if err != nil {
		return "", fmt.Errorf("profile dir: %w", err)
	}

	pw, err := playwright.Run()
	if err != nil {
		return "", fmt.Errorf("playwright: %w", err)
	}
	defer pw.Stop()

	headless := false
	browser, err := pw.Chromium.LaunchPersistentContext(profileDir, playwright.BrowserTypeLaunchPersistentContextOptions{
		Headless: playwright.Bool(headless),
		Args:     chromeArgs(),
	})
	if err != nil {
		return "", fmt.Errorf("browser: %w", err)
	}
	defer browser.Close()

	pages := browser.Pages()
	var page playwright.Page
	var pageErr error
	if len(pages) > 0 {
		page = pages[0]
	} else {
		page, pageErr = browser.NewPage()
		if pageErr != nil {
			return "", fmt.Errorf("new page: %w", pageErr)
		}
	}

	if _, err := page.Goto(spec.URL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return "", fmt.Errorf("navigate to %s: %w", spec.URL, err)
	}

	time.Sleep(2 * time.Second)

	text, err := page.InnerText("body")
	if err != nil {
		title, _ := page.Title()
		text = "Page title: " + title
	}
	if len(text) > 6000 {
		text = text[:6000] + "\n...(truncated)"
	}
	return strings.TrimSpace(text), nil
}

// BuildWebPrompt returns the bootstrap prompt for a web tool.
func BuildWebPrompt(spec config.ToolSpec, pageText string) string {
	var b strings.Builder
	b.WriteString("You are bootstrapping a web tool for ag.\n\n")
	b.WriteString("Web app: " + spec.Name + " (" + spec.URL + ")\n")
	if spec.Hint != "" {
		b.WriteString("Hint: " + spec.Hint + "\n")
	}
	b.WriteString("\nPage content (visible text):\n```\n")
	b.WriteString(pageText)
	b.WriteString("\n```\n\n")
	b.WriteString("Based on the page content and hint, define 3-5 useful actions for this web app.\n")
	b.WriteString("For each action provide:\n")
	b.WriteString("- name: short snake_case action name (e.g. search, list_top_stories)\n")
	b.WriteString("- description: what it does (1-2 sentences)\n")
	b.WriteString("- parameters: JSON Schema for the inputs\n\n")
	b.WriteString("Call config_manage with action=update_tool to save:\n")
	b.WriteString("- name: " + spec.Name + "\n")
	b.WriteString("- url: " + spec.URL + " (preserve exactly)\n")
	b.WriteString(`- description: a JSON array of action objects, prefixed with '__web_actions__:'` + "\n")
	b.WriteString("  Example:\n")
	b.WriteString(`  __web_actions__:[{"name":"search","description":"Search the site","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]`)
	b.WriteString("\n- examples: 2-3 example user prompts\n\n")
	b.WriteString("Also generate 1-2 starter context skills describing when to use this web tool.\n")
	b.WriteString("Call config_manage update_skill for each skill.\n\n")
	b.WriteString("Respond with a brief summary when done.")
	return b.String()
}
