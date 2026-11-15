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
	if width == 0 {
		return ""
	}
	text = ansi.Truncate(text, width, "…")
	style := ansi.NewStyle().ForegroundColor(lipgloss.Color(fg)).BackgroundColor(lipgloss.Color(bg))
	if bold {
		style = style.Bold()
	}
	// Lipgloss v2 passes nested SGR resets through. Restore this row's style
	// after colored fragments, or following text inherits the terminal's default
	// background (especially visible after HEAD badges on light surfaces).
	prefix := style.String()
	text = strings.NewReplacer("\x1b[m", "\x1b[m"+prefix, "\x1b[0m", "\x1b[0m"+prefix).Replace(text)
	return prefix + text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text))) + ansi.ResetStyle
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
	branchWidth := rightLimit - 3 - ansi.StringWidth(counts)
	var trailing string
	if branchWidth > 0 {
		name := ansi.Truncate(safeLine(branch), branchWidth, "…")
		trailing = line(name, ansi.StringWidth(name), c.Accent, c.Background, false) +
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
func (m *Model) historyVisible() bool {
	return m.width >= 100 || m.focus == "history"
}
func (m *Model) footerStatus() bool {
	if m.activity() == "" && !m.closing {
		return false
	}
	width := m.contentWidth()
	if m.width >= 100 {
		width /= 2
	}
	return !m.historyVisible() || m.historyStatusWidth(max(0, width-2*horizontalPadding)) < ansi.StringWidth(m.fetchStatus(m.colors()))
}
func (m *Model) footerHeight() int {
	height := 1 // Keyboard shortcuts; fetch status lives in the history header.
	if m.footerStatus() {
		height++
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
	c := m.colors()
	if m.dialog != "" {
		c = faded(c)
	}
	return m.detailContent(c, m.paneBackground("details", c))
}

func (m *Model) detailContent(c palette.Colors, bg string) []string {
	if m.details == nil {
		return nil
	}
	d := m.details
	width := max(1, m.paneWidth()-2*horizontalPadding)
	var rows []string
	wrap := func(text string) {
		rows = append(rows, strings.Split(ansi.Wrap(gitrepo.Safe(text), width, ""), "\n")...)
	}
	subject, body, _ := strings.Cut(strings.TrimRight(d.Message, "\n"), "\n")
	sha := d.SHA[:min(8, len(d.SHA))]
	heading := line(sha, len(sha), c.Accent, bg, false) + " " + safeLine(subject)
	rows = append(rows, strings.Split(ansi.Wrap(heading, width, ""), "\n")...)
	rows = append(rows, columns(safeLine(d.Author), relativeTime(d.AuthoredAt, m.now), width, width/2, c.Text, c.Muted, bg), "")
	if body = strings.Trim(body, "\n"); body != "" {
		wrap(body)
		rows = append(rows, "")
	}
	green := m.semanticColor(c, "#4ade80", "#15803d")
	red := errorColor(c)
	for _, file := range d.Files {
		statusColor := c.Muted
		switch {
		case strings.HasPrefix(file.Status, "A"):
			statusColor = green
		case strings.HasPrefix(file.Status, "D"):
			statusColor = red
		case strings.HasPrefix(file.Status, "M"):
			statusColor = m.semanticColor(c, "#fbbf24", "#a16207")
		case strings.HasPrefix(file.Status, "R"), strings.HasPrefix(file.Status, "C"):
			statusColor = c.Accent
		}
		counts := ""
		if file.Binary {
			counts = "binary"
		} else {
			if file.Added > 0 {
				text := fmt.Sprintf("+%d", file.Added)
				counts = line(text, len(text), green, bg, false)
			}
			if file.Deleted > 0 {
				if counts != "" {
					counts += " "
				}
				text := fmt.Sprintf("-%d", file.Deleted)
				counts += line(text, len(text), red, bg, false)
			}
		}
		// Reserve status and counts before shortening paths. Where space permits,
		// keep a path cell; on tiny panes counts may need truncation themselves.
		status := safeLine(file.Status)
		statusWidth := min(width, ansi.StringWidth(status))
		statusGap := min(horizontalPadding, width-statusWidth)
		remaining := width - statusWidth - statusGap
		countWidth := min(ansi.StringWidth(counts), max(0, remaining-1-horizontalPadding))
		countGap := 0
		if countWidth > 0 {
			countGap = horizontalPadding
		}
		pathWidth := remaining - countWidth - countGap
		name := changedFileName(file.Path, file.OldPath, pathWidth)
		rows = append(rows, line(status, statusWidth, statusColor, bg, false)+
			line("", statusGap, c.Text, bg, false)+line(name, pathWidth, c.Text, bg, false)+
			line("", countGap, c.Text, bg, false)+line(counts, countWidth, c.Muted, bg, false))
	}
	rows = append(rows, "")
	wrap(fmt.Sprintf("%s\nAuthor: %s <%s>\nAuthored: %s\nCommitter: %s <%s>\nCommitted: %s\nParents: %s\n\n%s", d.SHA, d.Author, d.AuthorEmail, d.AuthoredAt, d.Committer, d.CommitterEmail, d.CommittedAt, strings.Join(d.Parents, " "), d.Comparison))
	return rows
}

// boundedPath takes sanitized text. Keep the full basename when possible,
// spending spare cells on the leading path; long basenames retain their end
// (including the extension) and a shorter leading fragment.
func boundedPath(path string, width int) string {
	if width <= 0 {
		return ""
	}
	cells := ansi.StringWidth(path)
	if cells <= width {
		return path
	}
	base := path[strings.LastIndex(path, "/")+1:]
	tailWidth := min(ansi.StringWidth(base), width-1)
	if ansi.StringWidth(base) > width-1 {
		tailWidth = width - 1 - (width-1)/3
	}
	// The ANSI truncators in v0.11.9 count ASCII bases separately from
	// following selectors/keycaps. Measure whole clusters at both cuts instead.
	tail, tailCells := path, cells
	for tailCells > tailWidth {
		cluster, clusterWidth := ansi.FirstGraphemeCluster(tail, ansi.GraphemeWidth)
		tail = tail[len(cluster):]
		tailCells -= clusterWidth
	}
	headEnd, headCells := 0, 0
	for headEnd < len(path) {
		cluster, clusterWidth := ansi.FirstGraphemeCluster(path[headEnd:], ansi.GraphemeWidth)
		if headCells+clusterWidth > width-1-tailCells {
			break
		}
		headEnd += len(cluster)
		headCells += clusterWidth
	}
	return path[:headEnd] + "…" + tail
}

func changedFileName(path, oldPath string, width int) string {
	path, oldPath = safeLine(path), safeLine(oldPath)
	if oldPath == "" || width < 7 {
		return boundedPath(path, width)
	}
	// Share the space, returning unused cells from a short path to the other.
	available := width - ansi.StringWidth(" → ")
	oldWidth := min(ansi.StringWidth(oldPath), available/2)
	newWidth := min(ansi.StringWidth(path), available-oldWidth)
	oldWidth = min(ansi.StringWidth(oldPath), available-newWidth)
	return boundedPath(oldPath, oldWidth) + " → " + boundedPath(path, newWidth)
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
	height := m.paneHeight() - 2
	if m.selectionOutsideHistory() {
		height--
	}
	return max(0, len(m.detailLines())-max(1, height))
}
func (m *Model) loadingLabel(label string, c palette.Colors) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.Accent)).Render(strings.TrimSpace(m.spinner.View())) + " " + label
}

func (m *Model) paneBackground(kind string, c palette.Colors) string {
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
	return bg
}

type scopeSegment struct {
	scope      gitrepo.Scope
	start, end int
	label      string
}

// The same terminal-cell allocation drives rendering and pointer hit testing.
func (m *Model) scopeSegments() []scopeSegment {
	width := m.contentWidth()
	if m.width >= 100 {
		width /= 2
	}
	inner := max(0, width-2*horizontalPadding)
	all := min(5, (inner+1)/2)
	if inner >= 14 {
		all = 5
	}
	current := min(9, inner-all)
	return []scopeSegment{{gitrepo.All, 0, all, " all "}, {gitrepo.Current, all, all + current, " current "}}
}

func (m *Model) historyStatusWidth(width int) int {
	for _, segment := range m.scopeSegments() {
		width -= segment.end - segment.start
	}
	return max(0, width-1) // Leave a gap after the scope buttons.
}

func (m *Model) fetchStatus(c palette.Colors) string {
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
	return status
}

func (m *Model) historyHeader(width int, c palette.Colors, bg string) string {
	active := m.activeScope()
	var buttons string
	for _, segment := range m.scopeSegments() {
		fg, surface := c.Muted, c.Background
		if segment.scope == active {
			fg, surface = c.Text, c.Selection
		}
		buttons += line(segment.label, segment.end-segment.start, fg, surface, segment.scope == active)
	}
	remaining := max(0, width-ansi.StringWidth(buttons))
	status := ansi.Truncate(m.fetchStatus(c), m.historyStatusWidth(width), "…")
	return buttons + line("", remaining-ansi.StringWidth(status), c.Text, bg, false) + line(status, ansi.StringWidth(status), c.Muted, bg, false)
}

func (m *Model) pane(kind string, width, height int, c palette.Colors) string {
	bg := m.paneBackground(kind, c)
	inner := max(0, width-2*horizontalPadding)
	rows := []string{line("", width, c.Text, bg, false)}
	add := func(text string, bold bool) {
		rows = append(rows, line("", horizontalPadding, c.Text, bg, false)+line(text, inner, c.Text, bg, bold)+line("", horizontalPadding, c.Text, bg, false))
	}
	if kind == "history" {
		add(m.historyHeader(inner, c, bg), false)
		add("", false)
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
				marker = "▎"
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
			meta := sha + " • " + safeLine(commit.Author)
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
			addCommitRow(meta, strings.Join(tags, " "), c.Muted, c.Muted)
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
			lines := m.detailContent(c, bg)
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
	var footer []string
	if m.footerStatus() {
		status := m.fetchStatus(c)
		if width := max(0, w-2*horizontalPadding); ansi.StringWidth(status) > width {
			// Do not spend the only visible cells on a spinner or hide quit
			// behind the operation it is waiting for.
			status = m.activity()
			if m.closing {
				status = "waiting to quit"
				if ansi.StringWidth(status) > width {
					status = "Quit pending"
				}
			}
		}
		footer = append(footer, padded(status, c.Muted, c.Background))
	}
	if lines := m.footerNoticeLines(); len(lines) > 0 {
		edge := c.Accent
		if m.notice != "" && m.noticeWarning {
			edge = errorColor(c)
		}
		footer = append(footer, strings.Join(messageRows(lines, w, c, c.Background, edge), "\n"))
	}
	hotkeyBackground := c.Panel
	if hotkeyBackground == c.Background {
		hotkeyBackground = c.Active
	}
	footer = append(footer, padded("↑↓ Navigate   Tab Pane   Ctrl+P Commands   q Quit", c.Muted, hotkeyBackground))
	separator := line("", w, c.Text, c.Background, false)
	return lipgloss.NewStyle().Background(lipgloss.Color(c.Background)).Padding(1, horizontalPadding).Width(m.width).Height(m.height).MaxWidth(m.width).MaxHeight(m.height).Render(header + "\n" + separator + "\n" + panes + "\n" + separator + "\n" + strings.Join(footer, "\n"))
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
	if m.updateBusy() {
		parts = append(parts, strings.TrimSpace(ansi.Strip(m.spinner.View()))+" "+m.updates.status)
	}
	if m.notice != "" {
		parts = append(parts, m.notice)
		if m.noticeWarning {
			parts = append(parts, "Ctrl+P → Show errors and warnings")
		}
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
		// On the smallest viewport retain the actual operation/notification,
		// rather than replacing its only line with a navigation hint.
		if limit > 1 {
			hint := "… Ctrl+P for update commands"
			if m.noticeWarning && m.notice != "" {
				hint = "… Ctrl+P → Show errors and warnings"
			}
			lines[len(lines)-1] = hint
		}
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
					marker = "▎"
				}
				fg, markerFG := c.Text, c.Accent
				if a.disabled != "" {
					fg, markerFG = c.Muted, c.Muted
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
				rows = append(rows, line(marker, 1, markerFG, bg, false)+line(strings.Repeat(" ", horizontalPadding)+safeLine(a.title), left, fg, bg, false)+line(" "+suffix, inner-left-1, c.Muted, bg, false))
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
	view.MouseMode = tea.MouseModeCellMotion
	view.BackgroundColor = lipgloss.Color(c.Background)
	view.ForegroundColor = lipgloss.Color(c.Text)
	return view
}
