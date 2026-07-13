package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
	"github.com/sausheong/ag/config"
	"github.com/sausheong/ag/ui"
	harnesstool "github.com/sausheong/harness/tool"
)

func chromeArgs() []string {
	if runtime.GOOS == "linux" {
		return []string{"--no-sandbox"}
	}
	return nil
}

// WebAction is one bootstrapped action for a web tool (e.g. "search", "list_inbox").
type WebAction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// WebTool drives a web app via Playwright for a single named action.
type WebTool struct {
	Spec   config.ToolSpec
	Action WebAction
}

func (t *WebTool) Name() string {
	return "web_" + t.Spec.Name + "_" + t.Action.Name
}

func (t *WebTool) Description() string {
	return t.Action.Description
}

func (t *WebTool) Parameters() json.RawMessage {
	if len(t.Action.Parameters) > 0 {
		return t.Action.Parameters
	}
	return json.RawMessage(`{"type":"object","properties":{"instruction":{"type":"string","description":"What to do on the page"}},"required":["instruction"]}`)
}

func (t *WebTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *WebTool) Execute(ctx context.Context, raw json.RawMessage) (harnesstool.ToolResult, error) {
	profileDir, err := ProfileDir(t.Spec.Profile)
	if err != nil {
		return harnesstool.ToolResult{Output: "profile error: " + err.Error()}, nil
	}

	pw, err := playwright.Run()
	if err != nil {
		return harnesstool.ToolResult{Output: "playwright init error: " + err.Error()}, nil
	}
	defer pw.Stop()

	headless := t.Spec.Headless
	browser, err := pw.Chromium.LaunchPersistentContext(profileDir, playwright.BrowserTypeLaunchPersistentContextOptions{
		Headless: playwright.Bool(headless),
		Args:     chromeArgs(),
	})
	if err != nil {
		return harnesstool.ToolResult{Output: "browser launch error: " + err.Error()}, nil
	}
	defer browser.Close()

	pages := browser.Pages()
	var page playwright.Page
	if len(pages) > 0 {
		page = pages[0]
	} else {
		page, err = browser.NewPage()
		if err != nil {
			return harnesstool.ToolResult{Output: "new page error: " + err.Error()}, nil
		}
	}

	if _, err := page.Goto(t.Spec.URL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return harnesstool.ToolResult{Output: "navigation error: " + err.Error()}, nil
	}

	select {
	case <-ctx.Done():
		return harnesstool.ToolResult{Output: "cancelled"}, nil
	case <-time.After(2 * time.Second):
	}

	var params map[string]interface{}
	_ = json.Unmarshal(raw, &params)

	fmt.Println()
	fmt.Print(ui.RenderToolOutput(fmt.Sprintf("Executing web action %q on %s\n", t.Action.Name, t.Spec.URL)))

	result, err := t.executeAction(page, params)
	if err != nil {
		return harnesstool.ToolResult{Output: "execution error: " + err.Error()}, nil
	}
	return harnesstool.ToolResult{Output: result}, nil
}

func (t *WebTool) executeAction(page playwright.Page, params map[string]interface{}) (string, error) {
	action := t.Action.Name
	switch {
	case strings.Contains(action, "search") || strings.Contains(action, "find"):
		return webSearch(page, params)
	default:
		return webExtract(page)
	}
}

func webSearch(page playwright.Page, params map[string]interface{}) (string, error) {
	query := ""
	for _, k := range []string{"query", "q", "search", "text", "term"} {
		if v, ok := params[k]; ok {
			query = fmt.Sprintf("%v", v)
			break
		}
	}
	if query == "" {
		return webExtract(page)
	}

	selectors := []string{
		`input[type="search"]`,
		`input[name="q"]`,
		`input[placeholder*="search" i]`,
		`input[aria-label*="search" i]`,
		`[role="searchbox"]`,
		`input[type="text"]`,
	}
	for _, sel := range selectors {
		el, err := page.QuerySelector(sel)
		if err != nil || el == nil {
			continue
		}
		if err := el.Fill(query); err != nil {
			continue
		}
		if err := el.Press("Enter"); err != nil {
			continue
		}
		if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State: playwright.LoadStateNetworkidle,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "ag: warning: page load state error: %v\n", err)
		}
		time.Sleep(time.Second)
		return webExtract(page)
	}
	return webExtract(page)
}

func webExtract(page playwright.Page) (string, error) {
	text, err := page.InnerText("body")
	if err != nil {
		title, _ := page.Title()
		return "Page: " + title, nil
	}
	if len(text) > 4000 {
		text = text[:4000] + "\n...(truncated)"
	}
	return strings.TrimSpace(text), nil
}

// webActionsKey is the prefix used to serialise web actions inside Description.
const webActionsKey = "__web_actions__:"

// WebToolsFromSpec returns one WebTool per bootstrapped action in the spec.
// If no actions are stored, returns a single generic "browse" tool.
func WebToolsFromSpec(spec config.ToolSpec) []*WebTool {
	actions := loadWebActions(spec)
	if len(actions) == 0 {
		return []*WebTool{{
			Spec: spec,
			Action: WebAction{
				Name:        "browse",
				Description: "Navigate to " + spec.URL + " and extract page content.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"instruction":{"type":"string","description":"What to look for or do on the page"}}}`),
			},
		}}
	}
	tools := make([]*WebTool, len(actions))
	for i, a := range actions {
		tools[i] = &WebTool{Spec: spec, Action: a}
	}
	return tools
}

func loadWebActions(spec config.ToolSpec) []WebAction {
	if !strings.HasPrefix(spec.Description, webActionsKey) {
		return nil
	}
	payload := strings.TrimPrefix(spec.Description, webActionsKey)
	var actions []WebAction
	if err := json.Unmarshal([]byte(payload), &actions); err != nil {
		fmt.Fprintf(os.Stderr, "ag: warning: corrupt web actions for tool (JSON parse error: %v)\n", err)
		return nil
	}
	return actions
}

// StoreWebActions serialises actions into a Description string.
func StoreWebActions(actions []WebAction) (string, error) {
	b, err := json.Marshal(actions)
	if err != nil {
		return "", err
	}
	return webActionsKey + string(b), nil
}

var _ harnesstool.Tool = (*WebTool)(nil)
