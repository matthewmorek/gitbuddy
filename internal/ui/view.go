package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/palette"
)

const horizontalPadding = 2

func line(text string, width int, fg, bg string, bold bool) string {
	width = max(0, width)
	text = ansi.Truncate(text, width, "…")
	return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg)).Bold(bold).Width(width).MaxWidth(width).Render(text)
}
func safeLine(text string) string { return strings.ReplaceAll(gitrepo.Safe(text), "\n", "↵") }

// All widths are terminal cells, including any already-colored text fragments.
// The right field is bounded so long authors/refs cannot consume the whole row.
func columns(left, right string, width, rightLimit int, leftFG, rightFG, bg string) string {
	width = max(0, width)
	right = ansi.Truncate(right, max(0, min(width, rightLimit)), "…")
	rightWidth := ansi.StringWidth(right)
	gap := 0
	if rightWidth > 0 && left != "" {
		gap = min(horizontalPadding, width-rightWidth)
	}
	leftWidth := max(0, width-rightWidth-gap)
	return line(left, leftWidth, leftFG, bg, false) + line("", gap, leftFG, bg, false) + line(right, rightWidth, rightFG, bg, false)
}

func (m *Model) semanticColor(c palette.Colors, dark, light string) string {
	color := light
	if darkSurface(c) {
		color = dark
	}
	if m.dialog != "" {
		color = dim(color)
	}
	return color
}

func (m *Model) header(c palette.Colors) string {
	width := max(0, m.contentWidth()-2*horizontalPadding)
	branch := m.snapshot.Branch
	if branch == "" {
		branch = "detached HEAD"
		if m.snapshot.Head == "" {
			branch = "unborn branch"
			if m.loading {
				branch = "…"
			}
		}
	}
	counts, countsColor := "↑— ↓—", c.Muted
	if comparison := m.snapshot.Comparison; comparison != nil {
		counts = fmt.Sprintf("↑%d ↓%d", comparison.Ahead, comparison.Behind)
		countsColor = m.semanticColor(c, "#4ade80", "#15803d")
	}
	// Preserve at least the app name and separator before spending space on refs.
	rightLimit := max(0, width-min(width, len("Gitbuddy / "))-horizontalPadding)
	branchWidth := rightLimit - 2 - 3 - ansi.StringWidth(counts)
	var trailing string
	if branchWidth > 0 {
		name := ansi.Truncate(safeLine(branch), branchWidth, "…")
		trailing = line("⎇ ", 2, c.Muted, c.Background, false) +
			line(name, ansi.StringWidth(name), c.Accent, c.Background, false) +
			line(" | ", 3, c.Muted, c.Background, false) +
			line(counts, ansi.StringWidth(counts), countsColor, c.Background, false)
	} else {
		trailing = line(ansi.Truncate(counts, rightLimit, "…"), rightLimit, countsColor, c.Background, false)
	}
	leading := "Gitbuddy / " + safeLine(filepath.Base(m.repo.Root()))
	return line("", horizontalPadding, c.Text, c.Background, false) +
		columns(leading, trailing, width, rightLimit, c.Text, c.Text, c.Background) +
		line("", horizontalPadding, c.Text, c.Background, false)
}

func relativeTime(raw string, now time.Time) string {
	authored, err := time.Parse(time.RFC3339, raw)
	if err != nil || now.IsZero() {
		return "—"
	}
	// RFC3339 dates can be farther apart than time.Duration's ~292-year range.
	// Whole Unix seconds cover those dates without saturating their relative age.
	elapsed := now.Unix() - authored.Unix()
	future := authored.After(now)
	if future {
		elapsed = authored.Unix() - now.Unix()
	}
	const minute = int64(time.Minute / time.Second)
	const hour = 60 * minute
	const day = 24 * hour
	if elapsed < minute {
		return "just now"
	}
	var label string
	switch {
	case elapsed < hour:
		n := elapsed / minute
		label = fmt.Sprintf("%d min", n)
		if n != 1 {
			label += "s"
		}
	case elapsed < day:
		label = fmt.Sprintf("%dh", elapsed/hour)
	case elapsed < 7*day:
		n := elapsed / day
		label = fmt.Sprintf("%d day", n)
		if n != 1 {
			label += "s"
		}
	case elapsed < 14*day:
		if future {
			return "next week"
		}
		return "last week"
	default:
		n, unit := elapsed/(7*day), "week"
		if elapsed >= 365*day {
			n, unit = elapsed/(365*day), "year"
		} else if elapsed >= 30*day {
			n, unit = elapsed/(30*day), "month"
		}
		label = fmt.Sprintf("%d %s", n, unit)
		if n != 1 {
			label += "s"
		}
	}
	if future {
		return "in " + label
	}
	return label + " ago"
}

func (m *Model) colors() palette.Colors {
	mode := m.settings.Value.ColorMode
	if mode == "system" {
		mode = "light"
		if m.dark {
			mode = "dark"
		}
	}
	return m.themes.Resolve(m.settings.Value.Theme, mode)
}
func dim(hex string) string {
	var r, g, b int
	_, _ = fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("#%02x%02x%02x", r*3/4, g*3/4, b*3/4)
}
func darkSurface(c palette.Colors) bool {
	var r, g, b int
	_, _ = fmt.Sscanf(c.Background, "#%02x%02x%02x", &r, &g, &b)
	return r*2126+g*7152+b*722 < 128*10000
}
func faded(c palette.Colors) palette.Colors {
	return palette.Colors{Background: dim(c.Background), Panel: dim(c.Panel), Active: dim(c.Active), Selection: dim(c.Selection), Text: dim(c.Text), Muted: dim(c.Muted), Accent: dim(c.Accent)}
}
func (m *Model) contentWidth() int { return max(1, m.width-2*horizontalPadding) }
func (m *Model) compactFooter() bool {
	// A padded notice plus two footer rows needs at least 16 terminal rows to
	// retain one complete commit. Share status/shortcuts below that height.
	return m.height < 16 && (m.updates.status != "" || m.warnings() != "")
}
func (m *Model) footerHeight() int {
	height := 2 // Operation status and keyboard shortcuts.
	if m.compactFooter() {
		height--
	}
	if lines := m.footerNoticeLines(); len(lines) > 0 {
		height += len(lines) + 2 // One blank row inside each end of the rule.
	}
	return height
}

// Outer vertical padding, a header, and a separator above and below the panes.
func (m *Model) paneHeight() int  { return max(1, m.height-5-m.footerHeight()) }
func (m *Model) visibleRows() int { return max(1, (m.paneHeight()-4)/2) }
func (m *Model) ensureVisible() {
	for i, row := range m.rows {
		if row.SHA == m.selected {
			if i < m.top {
				m.top = i
			}
			if i >= m.top+m.visibleRows() {
				m.top = i - m.visibleRows() + 1
			}
			break
		}
	}
	m.top = max(0, min(m.top, max(0, len(m.rows)-m.visibleRows())))
}
func (m *Model) paneWidth() int {
	width := m.contentWidth()
	if m.width >= 100 {
		return width - width/2
	}
	return width
}
func (m *Model) detailLines() []string {
	if m.details == nil {
		return nil
	}
	d := m.details
	text := fmt.Sprintf("%s\n\n%s\nAuthor: %s <%s>\nAuthored: %s\nCommitter: %s <%s>\nCommitted: %s\nParents: %s\n\n%s\nChanged files\n", d.Message, d.SHA, d.Author, d.AuthorEmail, d.AuthoredAt, d.Committer, d.CommitterEmail, d.CommittedAt, strings.Join(d.Parents, " "), d.Comparison)
	for _, file := range d.Files {
		name := file.Path
		if file.OldPath != "" {
			name = file.OldPath + " → " + name
		}
		text += file.Status + "  " + name + "\n"
	}
	return strings.Split(ansi.Wrap(gitrepo.Safe(text), max(1, m.paneWidth()-2*horizontalPadding), ""), "\n")
}
func (m *Model) selectionOutsideHistory() bool {
	if m.selected == "" {
		return false
	}
	for _, row := range m.rows {
		if row.SHA == m.selected {
			return false
		}
	}
	return true
}
func (m *Model) maxDetailsTop() int {
	height := m.paneHeight() - 4
	if m.selectionOutsideHistory() {
		height--
	}
	return max(0, len(m.detailLines())-max(1, height))
}
func (m *Model) loadingLabel(label string, c palette.Colors) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.Accent)).Render(strings.TrimSpace(m.spinner.View())) + " " + label
}

func (m *Model) pane(kind string, width, height int, c palette.Colors) string {
	bg := c.Panel
	focused := c.Active
	// Keep existing custom theme files compatible: their Active token is the
	// darker surface. Dark terminals need the lighter surface to show focus.
	if darkSurface(c) {
		bg, focused = c.Active, c.Panel
	}
	if m.focus == kind {
		bg = focused
	}
	inner := max(0, width-2*horizontalPadding)
	rows := []string{line("", width, c.Text, bg, false)}
	add := func(text string, bold bool) {
		rows = append(rows, line("", horizontalPadding, c.Text, bg, false)+line(text, inner, c.Text, bg, bold)+line("", horizontalPadding, c.Text, bg, false))
	}
	title := "History"
	if kind == "history" {
		title += " · " + string(m.scope)
	}
	if kind == "details" {
		title = "Commit details"
	}
	add(title, m.focus == kind)
	add("", false)
	if kind == "history" {
		if len(m.rows) == 0 && !m.loading {
			add("No commits in this view", false)
		}
		for i := m.top; i < len(m.rows) && len(rows)+2 <= height-1; i++ {
			commit := m.rows[i]
			selected := commit.SHA == m.selected
			rowBG := bg
			if selected {
				rowBG = c.Selection
			}
			marker := " "
			if selected {
				marker = "▌"
			}
			indent := horizontalPadding
			if m.snapshot.Diverged && commit.Relation == gitrepo.LocalOnly {
				indent += 2
			}
			fg := c.Text
			if commit.Relation == gitrepo.Incoming {
				fg = c.Muted
			}
			subject := safeLine(commit.Subject)
			tags := []string{}
			for _, label := range commit.Labels {
				if label == "HEAD" {
					subject = line("[HEAD]", 6, m.semanticColor(c, "#facc15", "#a16207"), rowBG, false) + " " + subject
				} else if strings.HasPrefix(label, "tag: ") {
					tags = append(tags, "["+safeLine(strings.TrimPrefix(label, "tag: "))+"]")
				}
			}
			sha := commit.SHA[:min(8, len(commit.SHA))]
			meta := line(sha, len(sha), c.Muted, rowBG, false)
			if len(tags) > 0 {
				meta += "  " + strings.Join(tags, " ")
			}
			addCommitRow := func(left, right string, leftFG, rightFG string) {
				contentWidth := max(0, inner-1-indent)
				rows = append(rows,
					line("", horizontalPadding, c.Text, bg, false)+
						line(marker, 1, c.Accent, rowBG, false)+
						line("", indent, c.Text, rowBG, false)+
						columns(left, right, contentWidth, contentWidth/2, leftFG, rightFG, rowBG)+
						line("", horizontalPadding, c.Text, bg, false))
			}
			addCommitRow(subject, relativeTime(commit.AuthoredAt, m.now), fg, c.Muted)
			addCommitRow(meta, safeLine(commit.Author), fg, c.Muted)
		}
	} else {
		if m.selectionOutsideHistory() {
			add("Selected commit outside loaded history", false)
		}
		if m.details == nil {
			if m.slowDetails {
				add(m.loadingLabel("Loading commit...", c), false)
			} else if !m.detailsLoading {
				add("Select a commit", false)
			}
		} else {
			lines := m.detailLines()
			for i := min(m.detailsTop, len(lines)); i < len(lines) && len(rows) < height-1; i++ {
				add(lines[i], false)
			}
		}
	}
	for len(rows) < height {
		rows = append(rows, line("", width, c.Text, bg, false))
	}
	return strings.Join(rows[:min(height, len(rows))], "\n")
}

func (m *Model) baseView(c palette.Colors) string {
	w := m.contentWidth()
	padded := func(text string, fg, bg string) string {
		return line("", horizontalPadding, fg, bg, false) + line(text, max(0, w-2*horizontalPadding), fg, bg, false) + line("", horizontalPadding, fg, bg, false)
	}
	header := m.header(c)
	var panes string
	if m.width >= 100 {
		panes = lipgloss.JoinHorizontal(lipgloss.Top, m.pane("history", w/2, m.paneHeight(), c), m.pane("details", w-w/2, m.paneHeight(), c))
	} else {
		panes = m.pane(m.focus, w, m.paneHeight(), c)
	}
	status := "Not fetched this session"
	if len(m.status.Remotes) == 0 {
		status = "No remotes configured"
	}
	if !m.lastFetch.IsZero() {
		status = "Fetched " + m.lastFetch.Format("15:04:05")
	}
	if m.fetchFailed {
		status += " · last fetch failed"
	}
	if m.activity() != "" {
		status = m.loadingLabel(m.activity(), c)
	}
	if m.closing {
		status += " · waiting to quit"
	}
	if m.compactFooter() {
		status += " · Ctrl+P Commands · q Quit"
	}
	footer := padded(status, c.Muted, c.Background)
	if lines := m.footerNoticeLines(); len(lines) > 0 {
		edge := c.Accent
		if m.warnings() != "" {
			edge = errorColor(c)
		}
		footer += "\n" + strings.Join(messageRows(lines, w, c, c.Background, edge), "\n")
	}
	if !m.compactFooter() {
		hotkeyBackground := c.Panel
		if hotkeyBackground == c.Background {
			hotkeyBackground = c.Active
		}
		footer += "\n" + padded("↑↓ Navigate   Tab Pane   Ctrl+P Commands   q Quit", c.Muted, hotkeyBackground)
	}
	separator := line("", w, c.Text, c.Background, false)
	return lipgloss.NewStyle().Background(lipgloss.Color(c.Background)).Padding(1, horizontalPadding).Width(m.width).Height(m.height).MaxWidth(m.width).MaxHeight(m.height).Render(header + "\n" + separator + "\n" + panes + "\n" + separator + "\n" + footer)
}

func (m *Model) dialogWidth() int { return max(1, min(84, m.width-2*horizontalPadding)) }

func errorColor(c palette.Colors) string {
	if darkSurface(c) {
		return "#f87171"
	}
	return "#b91c1c"
}

func messagePadding(width int) int { return min(horizontalPadding, max(0, (width-2)/2)) }
func messageWidth(width int) int   { return max(1, width-1-2*messagePadding(width)) }

// The rule includes the vertical padding, not just the first wrapped text line.
func messageRows(lines []string, width int, c palette.Colors, bg, edge string) []string {
	padding := messagePadding(width)
	row := func(text string) string {
		return line("▎", 1, edge, bg, false) + line("", padding, c.Text, bg, false) + line(text, max(0, width-1-2*padding), c.Text, bg, false) + line("", padding, c.Text, bg, false)
	}
	rows := []string{row("")}
	for _, text := range lines {
		rows = append(rows, row(text))
	}
	return append(rows, row(""))
}

func (m *Model) footerNoticeLines() []string {
	var parts []string
	if m.updates.status != "" {
		text := m.updates.status
		if m.updateBusy() {
			text = strings.TrimSpace(ansi.Strip(m.spinner.View())) + " " + text
		}
		parts = append(parts, text)
	}
	warnings := m.warnings()
	if warnings != "" {
		parts = append(parts, warnings, "Ctrl+P → Show errors and warnings")
	}
	if len(parts) == 0 {
		return nil
	}
	lines := strings.Split(ansi.Wrap(gitrepo.Safe(strings.Join(parts, "\n")), messageWidth(m.contentWidth()), ""), "\n")
	// Leave room for a complete commit row on compact terminals. Full diagnostics
	// remain scrollable in the errors dialog instead of swallowing the history.
	limit := max(1, min(4, m.height-15))
	if len(lines) > limit {
		lines = lines[:limit]
		hint := "… Ctrl+P for update commands"
		if warnings != "" {
			hint = "… Ctrl+P → Show errors and warnings"
		}
		lines[len(lines)-1] = hint
	}
	return lines
}

// Rendering and keyboard scrolling share the same wrapped diagnostic lines.
func (m *Model) errorLines() []string {
	text := gitrepo.Safe(m.warnings())
	if text == "" {
		text = "No errors or warnings"
	}
	return strings.Split(ansi.Wrap(text, messageWidth(max(1, m.dialogWidth()-2*horizontalPadding)), ""), "\n")
}

func (m *Model) dialogView(c palette.Colors, width, height int) string {
	inner := max(1, width-2*horizontalPadding)
	rows := []string{}
	add := func(text string) { rows = append(rows, line(text, inner, c.Text, c.Panel, false)) }
	title := "Commands"
	switch m.dialog {
	case "confirm-update":
		title = "Install update · restart is optional"
	case "settings":
		title = "Settings"
	case "themes":
		title = "Theme"
	case "modes":
		title = "Color mode"
	case "max":
		title = "Maximum commits"
	case "interval":
		title = "Fetch interval (minutes)"
	case "errors":
		title = "Errors and warnings"
	}
	rows = append(rows, line(title, inner, c.Text, c.Panel, true))
	if m.dialog == "errors" {
		lines := m.errorLines()
		start := min(m.errorTop, max(0, len(lines)-1))
		end := min(len(lines), start+max(1, height-6))
		rows = append(rows, messageRows(lines[start:end], inner, c, c.Panel, errorColor(c))...)
		add("Esc Close · ↑↓ Scroll")
	} else {
		input := m.input
		input.SetWidth(max(1, inner-1))
		styles := textinput.DefaultStyles(m.dark)
		styles.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Text)).Background(lipgloss.Color(c.Panel))
		styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Muted)).Background(lipgloss.Color(c.Panel))
		styles.Cursor.Color = lipgloss.Color(c.Accent)
		input.SetStyles(styles)
		add(input.View())
		add("")
		if m.dialog == "max" || m.dialog == "interval" {
			add("Enter a positive whole number")
			add("Enter Save · Esc Cancel")
		} else {
			options := m.options()
			choice := min(m.choice, max(0, len(options)-1))
			hint := "↑↓ Navigate · Enter Select · Esc Close"
			hintRows := []string{line(hint, inner, c.Text, c.Panel, false)}
			if len(options) > 0 && options[choice].disabled != "" {
				lines := strings.Split(ansi.Wrap(gitrepo.Safe(options[choice].disabled), messageWidth(inner), ""), "\n")
				lines = lines[:min(len(lines), max(1, min(4, height-9)))]
				hintRows = messageRows(lines, inner, c, c.Panel, errorColor(c))
			}
			visible := max(1, min(12, height-2-len(rows)-1-len(hintRows)))
			top := max(0, choice-visible+1)
			for i := top; i < len(options) && i < top+visible; i++ {
				a := options[i]
				bg := c.Panel
				marker := " "
				if i == choice {
					bg = c.Selection
					marker = "▌"
				}
				suffix := a.category
				if a.value != "" {
					suffix = a.value + " · " + suffix
				}
				if a.shortcut != "" {
					suffix = a.shortcut + " · " + suffix
				}
				suffix = ansi.Truncate(suffix, max(1, inner*45/100), "…")
				left := max(0, inner-2-ansi.StringWidth(suffix))
				rows = append(rows, line(marker, 1, c.Accent, bg, false)+line(strings.Repeat(" ", horizontalPadding)+safeLine(a.title), left, c.Text, bg, false)+line(" "+suffix, inner-left-1, c.Muted, bg, false))
			}
			if len(options) == 0 {
				add("No matching commands")
			}
			add("")
			rows = append(rows, hintRows...)
		}
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(c.Panel)).Padding(1, horizontalPadding).Width(width).MaxWidth(width).MaxHeight(height).Render(strings.Join(rows, "\n"))
}

func (m *Model) View() tea.View {
	c := m.colors()
	baseColors := c
	if m.dialog != "" {
		baseColors = faded(c)
	}
	content := m.baseView(baseColors)
	if m.dialog != "" {
		w := m.dialogWidth()
		y := max(1, m.height/6)
		h := max(1, m.height-y-1)
		modal := m.dialogView(c, w, h)
		content = lipgloss.NewCompositor(lipgloss.NewLayer(content), lipgloss.NewLayer(modal).X((m.width-w)/2).Y(y).Z(1)).Render()
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.BackgroundColor = lipgloss.Color(c.Background)
	view.ForegroundColor = lipgloss.Color(c.Text)
	return view
}
