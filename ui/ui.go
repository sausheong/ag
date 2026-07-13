package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// termWidth returns the current terminal width, defaulting to 100 if undetectable.
func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 100
}

// wrapCell wraps text to maxW runes and returns lines, each exactly maxW runes wide
// (padded with spaces). Continuation lines have leftPad spaces prepended so they
// align under the cell start rather than column 0.
func wrapCell(text string, maxW, leftPad int) []string {
	if maxW <= 0 {
		maxW = 20
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{strings.Repeat(" ", maxW)}
	}
	var lines []string
	line := ""
	for _, w := range words {
		if line == "" {
			line = w
		} else if len(line)+1+len(w) <= maxW {
			line += " " + w
		} else {
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	pad := strings.Repeat(" ", leftPad)
	out := make([]string, len(lines))
	for i, l := range lines {
		if i == 0 {
			// First line: pad to maxW
			if len(l) < maxW {
				l += strings.Repeat(" ", maxW-len(l))
			}
			out[i] = l
		} else {
			// Continuation: left-pad + content + right-pad to maxW
			content := l
			if len(content) < maxW {
				content += strings.Repeat(" ", maxW-len(content))
			}
			out[i] = pad + content
		}
	}
	return out
}

// MARK: - Markdown line renderer

var (
	styleMdH1 = lipgloss.NewStyle().Bold(true).Foreground(colorCyan).
			BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).BorderForeground(colorCyan)
	styleMdH2    = lipgloss.NewStyle().Bold(true).Foreground(colorCyan)
	styleMdH3    = lipgloss.NewStyle().Bold(true).Foreground(colorDimFg)
	styleMdCode  = lipgloss.NewStyle().Foreground(colorYellow)
	styleMdBulk  = lipgloss.NewStyle().Foreground(colorDimFg)
	styleMdHRule = lipgloss.NewStyle().Foreground(colorDimFg)
	styleMdBold  = lipgloss.NewStyle().Bold(true)
	styleMdItal  = lipgloss.NewStyle().Italic(true)

	styleToolLine = lipgloss.NewStyle().Foreground(colorDimFg).PaddingLeft(2)
)

// LineRenderer buffers agent text deltas by line and applies markdown styling
// as each line completes. Call Feed for each delta, Flush at end-of-turn.
type LineRenderer struct {
	buf          strings.Builder
	inCodeBlock  bool
	codeLanguage string
	tableRows    []string // buffered raw table lines (including separator)
}

// Feed accepts a text delta, prints any complete lines with styling applied.
func (r *LineRenderer) Feed(delta string) {
	r.buf.WriteString(delta)
	for {
		s := r.buf.String()
		nl := strings.Index(s, "\n")
		if nl < 0 {
			break
		}
		line := s[:nl]
		r.buf.Reset()
		r.buf.WriteString(s[nl+1:])
		r.consumeLine(line)
	}
}

// Flush prints any remaining buffered text (no trailing newline from the model).
func (r *LineRenderer) Flush() {
	if s := strings.TrimRight(r.buf.String(), "\r"); s != "" {
		r.consumeLine(s)
		r.buf.Reset()
	}
	r.flushTable()
}

// consumeLine routes a complete line to table buffering or immediate rendering.
func (r *LineRenderer) consumeLine(line string) {
	if r.inCodeBlock {
		fmt.Println(r.renderLine(line))
		return
	}
	isTableRow := strings.HasPrefix(strings.TrimSpace(line), "|")
	if isTableRow {
		r.tableRows = append(r.tableRows, line)
		return
	}
	// Non-table line — flush any buffered table first.
	r.flushTable()
	rendered := r.renderLine(line)
	if rendered != "" {
		fmt.Println(rendered)
	}
}

// flushTable renders buffered table rows with lipgloss and clears the buffer.
func (r *LineRenderer) flushTable() {
	if len(r.tableRows) == 0 {
		return
	}
	fmt.Print(renderMarkdownTable(r.tableRows))
	r.tableRows = nil
}

func (r *LineRenderer) renderLine(line string) string {
	// Code fence toggle
	if strings.HasPrefix(line, "```") {
		if r.inCodeBlock {
			r.inCodeBlock = false
			r.codeLanguage = ""
			return styleMdHRule.Render(strings.Repeat("─", 40))
		}
		r.inCodeBlock = true
		r.codeLanguage = strings.TrimPrefix(line, "```")
		label := "code"
		if r.codeLanguage != "" {
			label = r.codeLanguage
		}
		return styleMdCode.Render("┌─ " + label + " " + strings.Repeat("─", max(0, 38-len(label))))
	}
	if r.inCodeBlock {
		return styleMdCode.Render("│ " + line)
	}

	// Horizontal rule — only for explicit --- or *** markers.
	// Lines made of ─ characters are table separators emitted by the agent's own
	// table rendering; the table renderer already adds its own separator line.
	if line == "---" || line == "***" {
		return styleMdHRule.Render(strings.Repeat("─", 60))
	}
	// Skip bare ─ separator lines — they're redundant with the table renderer.
	if len(line) > 2 && strings.TrimLeft(line, "─") == "" {
		return ""
	}

	// Headers
	if strings.HasPrefix(line, "### ") {
		return styleMdH3.Render(strings.TrimPrefix(line, "### "))
	}
	if strings.HasPrefix(line, "## ") {
		return styleMdH2.Render(strings.TrimPrefix(line, "## "))
	}
	if strings.HasPrefix(line, "# ") {
		return styleMdH1.Render(strings.TrimPrefix(line, "# "))
	}

	// Bullet lists
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return "  " + styleMdBulk.Render("•") + " " + renderInline(line[2:])
	}
	if len(line) > 2 && line[1] == '.' && line[2] == ' ' && line[0] >= '0' && line[0] <= '9' {
		return "  " + styleMdBulk.Render(string(line[0])+".") + " " + renderInline(line[3:])
	}

	// Blockquote
	if strings.HasPrefix(line, "> ") {
		return styleMdHRule.Render("│ ") + renderInline(strings.TrimPrefix(line, "> "))
	}

	return renderInline(line)
}

// renderInline applies bold, italic, and inline code styling within a line.
func renderInline(s string) string {
	var out strings.Builder
	for len(s) > 0 {
		switch {
		case strings.HasPrefix(s, "**") || strings.HasPrefix(s, "__"):
			delim := s[:2]
			end := strings.Index(s[2:], delim)
			if end >= 0 {
				out.WriteString(styleMdBold.Render(s[2 : 2+end]))
				s = s[2+end+2:]
			} else {
				out.WriteString(s[:1])
				s = s[1:]
			}
		case strings.HasPrefix(s, "*") || strings.HasPrefix(s, "_"):
			delim := s[:1]
			end := strings.Index(s[1:], delim)
			if end >= 0 {
				out.WriteString(styleMdItal.Render(s[1 : 1+end]))
				s = s[1+end+1:]
			} else {
				out.WriteString(s[:1])
				s = s[1:]
			}
		case strings.HasPrefix(s, "`"):
			end := strings.Index(s[1:], "`")
			if end >= 0 {
				out.WriteString(styleMdCode.Render(s[1 : 1+end]))
				s = s[1+end+1:]
			} else {
				out.WriteString(s[:1])
				s = s[1:]
			}
		default:
			out.WriteByte(s[0])
			s = s[1:]
		}
	}
	return out.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// parseTableRows splits raw markdown table lines into a 2D slice of cells,
// skipping the separator row (|---|---|).
func parseTableRows(lines []string) [][]string {
	var rows [][]string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip separator rows like |---|---|
		if strings.ContainsRune(line, '|') {
			inner := strings.Trim(line, "|")
			if strings.TrimLeft(strings.ReplaceAll(inner, " ", ""), "-:|") == "" {
				continue
			}
		}
		parts := strings.Split(strings.Trim(line, "|"), "|")
		cells := make([]string, len(parts))
		for i, p := range parts {
			cells[i] = strings.TrimSpace(p)
		}
		rows = append(rows, cells)
	}
	return rows
}

// renderMarkdownTable parses raw markdown table lines and renders them with lipgloss.
func renderMarkdownTable(lines []string) string {
	rows := parseTableRows(lines)
	if len(rows) == 0 {
		return ""
	}

	// Compute max column widths from all rows.
	ncols := 0
	for _, r := range rows {
		if len(r) > ncols {
			ncols = len(r)
		}
	}
	widths := make([]int, ncols)
	for _, row := range rows {
		for j, cell := range row {
			// renderInline to account for styled text visible width.
			if len(cell) > widths[j] {
				widths[j] = len(cell)
			}
		}
	}

	colStyles := make([]lipgloss.Style, ncols)
	for j, w := range widths {
		colStyles[j] = lipgloss.NewStyle().Width(w + 2) // +2 padding
	}

	var b strings.Builder
	for i, row := range rows {
		for j := 0; j < ncols; j++ {
			cell := ""
			if j < len(row) {
				cell = row[j]
			}
			if i == 0 {
				// Header row
				b.WriteString(styleTableHeader.Render(colStyles[j].Render(renderInline(cell))))
			} else {
				b.WriteString(colStyles[j].Render(renderInline(cell)))
			}
		}
		b.WriteString("\n")
		if i == 0 {
			// Separator after header
			total := 0
			for _, w := range widths {
				total += w + 2
			}
			b.WriteString(styleHelp.Render(strings.Repeat("─", total)) + "\n")
		}
	}
	return b.String()
}

// RenderToolOutput renders raw subprocess output, detecting and styling
// markdown tables; other lines are rendered dim and indented.
func RenderToolOutput(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var b strings.Builder
	var tableBuf []string

	flushTable := func() {
		if len(tableBuf) == 0 {
			return
		}
		b.WriteString(renderMarkdownTable(tableBuf))
		tableBuf = nil
	}

	for _, line := range lines {
		isTable := strings.HasPrefix(strings.TrimSpace(line), "|")
		if isTable {
			tableBuf = append(tableBuf, line)
		} else {
			flushTable()
			b.WriteString(styleToolLine.Render(line) + "\n")
		}
	}
	flushTable()
	return b.String()
}

var (
	colorCyan   = lipgloss.AdaptiveColor{Light: "#007A7A", Dark: "#00AFAF"}
	colorGreen  = lipgloss.AdaptiveColor{Light: "#007A3A", Dark: "#00AF5F"}
	colorRed    = lipgloss.AdaptiveColor{Light: "#CC0000", Dark: "#FF5F5F"}
	colorYellow = lipgloss.AdaptiveColor{Light: "#AA7700", Dark: "#FFAF00"}
	colorDimFg  = lipgloss.AdaptiveColor{Light: "#666666", Dark: "#AAAAAA"}
	colorDimBg  = lipgloss.AdaptiveColor{Light: "#E0E0E0", Dark: "#3A3A3A"}
	colorUsage  = lipgloss.AdaptiveColor{Light: "#888888", Dark: "#666666"}
	colorHint   = lipgloss.AdaptiveColor{Light: "#888888", Dark: "#999999"}

	stylePrompt = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCyan)

	styleToolBadge = lipgloss.NewStyle().
			Background(colorDimBg).
			Foreground(colorDimFg).
			PaddingLeft(1).
			PaddingRight(1)

	styleToolOK = lipgloss.NewStyle().
			Foreground(colorGreen)

	styleUsage = lipgloss.NewStyle().
			Italic(true).
			Foreground(colorUsage)

	styleError = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorRed)

	styleBootstrapName = lipgloss.NewStyle().
				Bold(true)

	styleBootstrapOK = lipgloss.NewStyle().
				Foreground(colorGreen)

	styleBootstrapWarn = lipgloss.NewStyle().
				Foreground(colorYellow)

	styleBootstrapHeader = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorCyan)

	styleTableHeader = lipgloss.NewStyle().
				Bold(true)

	styleTableType = lipgloss.NewStyle().
			Foreground(colorHint)

	styleWelcome = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCyan)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorHint)
)

// ConfigInfo renders a summary of the current ag.yaml configuration.
func ConfigInfo(model, authToken, baseURL, cfgPath string) string {
	var b strings.Builder
	b.WriteString(styleTableHeader.Render("Configuration") + "  " + styleHelp.Render(cfgPath) + "\n\n")
	label := lipgloss.NewStyle().Width(12).Foreground(colorDimFg)
	val := lipgloss.NewStyle().Bold(true)
	masked := "●●●●●●●●"
	if authToken == "" {
		masked = styleHelp.Render("(not set)")
	}
	b.WriteString("  " + label.Render("model") + val.Render(model) + "\n")
	b.WriteString("  " + label.Render("auth_token") + masked + "\n")
	if baseURL != "" {
		b.WriteString("  " + label.Render("base_url") + val.Render(baseURL) + "\n")
	}
	return b.String()
}

// ToolsList renders the list of registered tools.
func ToolsList(tools []struct{ Name, Bin, URL, Desc string }) string {
	if len(tools) == 0 {
		return styleHelp.Render("No tools configured. Run 'ag add tool <name> <bin>' to add one.")
	}
	nameW, binW := 6, 4
	for _, t := range tools {
		if len(t.Name) > nameW {
			nameW = len(t.Name)
		}
		binOrURL := t.Bin
		if binOrURL == "" {
			binOrURL = t.URL
		}
		if len(binOrURL) > binW {
			binW = len(binOrURL)
		}
	}
	nameW += 2
	binW += 2
	descW := termWidth() - nameW - binW - 2
	if descW < 20 {
		descW = 20
	}
	colName := lipgloss.NewStyle().Width(nameW)
	colBin := lipgloss.NewStyle().Width(binW)
	colBinDim := colBin.Foreground(colorDimFg)
	colDesc := lipgloss.NewStyle().Width(descW)

	_ = colDesc // unused now that we use wrapCell
	leftPad := nameW + binW
	var b strings.Builder
	b.WriteString(
		styleTableHeader.Render(colName.Render("TOOL"))+
			styleTableHeader.Render(colBin.Render("BIN"))+
			styleTableHeader.Render("DESCRIPTION")+"\n",
	)
	b.WriteString(styleHelp.Render(strings.Repeat("─", nameW+binW+descW)) + "\n")
	for _, t := range tools {
		binOrURL := t.Bin
		if binOrURL == "" {
			binOrURL = t.URL
		}
		desc := t.Desc
		if desc == "" {
			desc = "(not bootstrapped)"
		}
		descLines := wrapCell(desc, descW, leftPad)
		for i, dl := range descLines {
			if i == 0 {
				b.WriteString(colName.Render(t.Name) + colBinDim.Render(binOrURL) + dl + "\n")
			} else {
				b.WriteString(dl + "\n")
			}
		}
	}
	return b.String()
}

// MCPList renders the list of configured MCP servers.
func MCPList(servers []struct{ Name, Transport string }) string {
	if len(servers) == 0 {
		return styleHelp.Render("No MCP servers configured. Run 'ag add mcp <name> --command <cmd>' to add one.")
	}
	colName := lipgloss.NewStyle().Width(22)
	var b strings.Builder
	b.WriteString(
		styleTableHeader.Render(colName.Render("SERVER")) +
			styleTableHeader.Render("TRANSPORT") + "\n",
	)
	b.WriteString(styleHelp.Render(strings.Repeat("─", 60)) + "\n")
	for _, s := range servers {
		b.WriteString(colName.Render(s.Name) + s.Transport + "\n")
	}
	return b.String()
}

// Prompt returns the styled "> " prompt string.
func Prompt() string {
	return stylePrompt.Render("> ")
}

// Welcome returns the styled welcome banner.
func Welcome() string {
	return styleWelcome.Render("ag") + " — type a message, " +
		styleHelp.Render("/help") + " for help, " +
		styleHelp.Render("/exit") + " or blank line to quit"
}

// Help returns the styled /help text.
func Help() string {
	cmds := []struct{ cmd, desc string }{
		{"/help", "show this help"},
		{"/exit", "exit the REPL"},
		{"/config", "show current configuration (model, auth, base_url)"},
		{"/tools", "list configured CLI tools"},
		{"/skills", "list learned skills"},
		{"/mcp", "list configured MCP servers"},
	}
	// Use Width() so padding is applied to the visible width, not the byte width.
	cmdStyle := lipgloss.NewStyle().Width(10)
	var b strings.Builder
	b.WriteString(styleTableHeader.Render("ag REPL commands") + "\n")
	for _, c := range cmds {
		b.WriteString("  " + stylePrompt.Render(cmdStyle.Render(c.cmd)) + " " + styleHelp.Render(c.desc) + "\n")
	}
	b.WriteString("\n" + styleHelp.Render("(blank line or Ctrl-D also exits)"))
	return b.String()
}

// ToolCall returns the styled tool call badge line.
func ToolCall(name string) string {
	return "\n" + styleToolBadge.Render("◆ "+name) + " "
}

// ToolOK returns the styled success checkmark.
func ToolOK() string {
	return styleToolOK.Render("✓")
}

// ToolOutput renders the output from a tool call, dim and indented.
func ToolOutput(output string) string {
	style := lipgloss.NewStyle().Foreground(colorDimFg).PaddingLeft(2)
	return "\n" + style.Render(output)
}

// TokenUsage returns the styled token usage footer.
func TokenUsage(in, out int) string {
	return "\n" + styleUsage.Render(fmt.Sprintf("[%d in / %d out tokens]", in, out))
}

// Error returns a styled inline error string.
func Error(msg string) string {
	return styleError.Render("error: " + msg)
}

// FatalError returns a styled fatal error string (for os.Stderr).
func FatalError(msg string) string {
	return styleError.Render(msg)
}

// BootstrapStep returns the styled step prefix ("  name... ").
func BootstrapStep(name string) string {
	return "  " + styleBootstrapName.Render(name) + "... "
}

// BootstrapOK returns the styled "ok" suffix.
func BootstrapOK() string {
	return styleBootstrapOK.Render("ok")
}

// BootstrapWarning returns the styled warning message.
func BootstrapWarning(msg string) string {
	return styleBootstrapWarn.Render("warning: " + msg)
}

// BootstrapHeader returns a styled section header.
func BootstrapHeader(msg string) string {
	return styleBootstrapHeader.Render(msg)
}

// SkillRow is one row in the skills table.
type SkillRow struct {
	Name        string
	Type        string
	Description string
}

// SkillsTable renders the full skills table as a string.
// Width() is used instead of %-Ns so padding is based on visible width, not byte width.
func SkillsTable(rows []SkillRow) string {
	if len(rows) == 0 {
		return styleHelp.Render("No skills learned yet.")
	}

	// Auto-size columns from content.
	nameW, typeW := 4, 4
	for _, r := range rows {
		if len(r.Name) > nameW {
			nameW = len(r.Name)
		}
		typ := r.Type
		if typ == "" {
			typ = "context"
		}
		if len(typ) > typeW {
			typeW = len(typ)
		}
	}
	nameW += 2
	typeW += 2
	descW := termWidth() - nameW - typeW - 2
	if descW < 20 {
		descW = 20
	}
	colName := lipgloss.NewStyle().Width(nameW)
	colType := lipgloss.NewStyle().Width(typeW)
	colTypeDim := colType.Foreground(colorHint)
	colDesc := lipgloss.NewStyle().Width(descW)

	_ = colDesc
	leftPad := nameW + typeW
	var b strings.Builder
	b.WriteString(
		styleTableHeader.Render(colName.Render("NAME"))+
			styleTableHeader.Render(colType.Render("TYPE"))+
			styleTableHeader.Render("DESCRIPTION")+"\n",
	)
	b.WriteString(styleHelp.Render(strings.Repeat("─", nameW+typeW+descW)) + "\n")
	for _, r := range rows {
		typ := r.Type
		if typ == "" {
			typ = "context"
		}
		desc := r.Description
		if desc == "" {
			desc = "(no description)"
		}
		descLines := wrapCell(desc, descW, leftPad)
		for i, dl := range descLines {
			if i == 0 {
				b.WriteString(colName.Render(r.Name) + colTypeDim.Render(typ) + dl + "\n")
			} else {
				b.WriteString(dl + "\n")
			}
		}
	}
	return b.String()
}
