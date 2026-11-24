package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/matthewmorek/gitbuddy/internal/palette"
)

const (
	modalBack     = "Backspace Back · Esc Close"
	modalTextBack = "Backspace (empty) Back · Esc Close"
)

// Rendering and keyboard scrolling share the same wrapped diagnostic lines.
func (m *Model) errorLines() []string {
	text := m.warnings()
	if text == "" {
		text = "No errors or warnings"
	}
	return wrapModalText(text, messageWidth(m.dialogWidth()-2*horizontalPadding))
}

func wrapModalText(text string, width int) []string {
	if text == "" {
		return nil
	}
	return strings.Split(ansi.Wrap(safeText(text), max(1, width), ""), "\n")
}

func (m *Model) dialogView(c palette.Colors, width, height int) string {
	page := m.modals.current()
	inner := max(1, width-2*horizontalPadding)
	room := max(1, height-2) // Outside the panel's vertical padding.
	rows := []string{line(modalTitle(page), inner, c.Text, c.Panel, true)}
	add := func(text string) { rows = append(rows, line(text, inner, c.Text, c.Panel, false)) }
	addWrapped := func(text string) {
		for _, row := range wrapModalText(text, inner) {
			add(row)
		}
	}
	spacious := room >= 9
	if spacious {
		add("")
	}
	if page.input != nil {
		input := *page.input
		input.SetWidth(max(1, inner-1))
		styles := textinput.DefaultStyles(m.dark)
		styles.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Text)).Background(lipgloss.Color(c.Panel))
		styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Muted)).Background(lipgloss.Color(c.Panel))
		styles.Cursor.Color = lipgloss.Color(c.Accent)
		input.SetStyles(styles)
		add(input.View())
	}
	switch page.kind {
	case modalStashApply, modalStashDelete:
		add(stashSubject(page.stash))
		add(stashMetadata(page.stash))
		confirm, effect := "Apply", "Restore the saved changes and keep the stash."
		if page.kind == modalStashDelete {
			confirm, effect = "Delete", "Remove the selected stash without applying it."
		}
		addWrapped(effect)
		button := func(label string, selected bool) string {
			fg, bg, marker := c.Muted, c.Panel, " "
			if selected {
				fg, bg, marker = c.Text, c.Selection, "▎"
			}
			return line(marker, 1, c.Accent, bg, false) + line(" "+label+" ", len(label)+2, fg, bg, selected)
		}
		add(button("Cancel", !page.confirm) + "  " + button(confirm, page.confirm))
		addWrapped("←→ Choose · Enter Select · Backspace/Esc Cancel")
	case modalErrors:
		lines := m.errorLines()
		hints := modalHints("↑↓ Scroll", modalBack, inner)
		start := min(page.top, len(lines)-1)
		end := min(len(lines), start+max(1, room-len(rows)-len(hints)-2))
		rows = append(rows, messageRows(lines[start:end], inner, c, c.Panel, errorColor(c))...)
		for _, hint := range hints {
			add(hint)
		}
	case modalNumber, modalStashNote:
		if spacious {
			add("")
		}
		if page.kind == modalNumber {
			rows = append(rows, modalNotices(wrapModalText(page.validation, inner-2), c, inner)...)
			bounds := "Use a positive whole number (up to 9,007,199,254,740,991)."
			if page.setting == settingInterval {
				bounds = "Use a whole number from 1 to 1,440 minutes."
			}
			addWrapped(bounds)
			for _, hint := range modalHints("Enter Save", modalTextBack, inner) {
				add(hint)
			}
		} else {
			disabled := m.stashDisabled(true)
			rows = append(rows, modalNotices(fitModalText(disabled, inner-2, 3), c, inner)...)
			addWrapped(stashNoteDescription)
			keys := ""
			if disabled == "" {
				keys = "Enter Stash"
			}
			for _, hint := range modalHints(keys, modalTextBack, inner) {
				add(hint)
			}
		}
	default:
		list := m.modalList(page, m.options(), inner, room)
		if spacious {
			add("")
		}
		end := min(len(list.rows), list.top+list.visible)
		for i := list.top; i < end; i++ {
			row := list.rows[i]
			if row.option.id != "" {
				rows = append(rows, modalOptionRow(page.kind, row.option, i == list.selected, inner, c))
			} else if row.heading != "" && i+1 < end {
				rows = append(rows, line(row.heading, inner, c.Accent, c.Panel, true))
			} else if i > list.top && i+1 < end {
				add("")
			}
		}
		if spacious {
			add("")
		}
		rows = append(rows, modalNotices(list.notices, c, inner)...)
		for _, text := range list.help {
			add(text)
		}
		for _, hint := range list.hints {
			rows = append(rows, line(hint, inner, c.Muted, c.Panel, false))
		}
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(c.Panel)).Padding(1, horizontalPadding).Width(width).MaxWidth(width).MaxHeight(height).Render(strings.Join(rows, "\n"))
}

func modalTitle(page *modalPage) string {
	switch page.kind {
	case modalSettings:
		return "Settings"
	case modalThemes:
		return "Theme"
	case modalNumber:
		if page.setting == settingInterval {
			return "Fetch interval (minutes)"
		}
		return "Maximum commits"
	case modalErrors:
		return "Errors and warnings"
	case modalStashNote:
		return "Stash all"
	case modalStashApply:
		return "Apply stash?"
	case modalStashDelete:
		return "Delete stash?"
	default:
		return "Commands"
	}
}

func modalNotices(text []string, c palette.Colors, width int) []string {
	rows := make([]string, 0, len(text))
	for _, row := range text {
		rows = append(rows, line("▎", 1, errorColor(c), c.Panel, false)+line(" "+row, width-1, c.Text, c.Panel, false))
	}
	return rows
}

func modalHints(keys, back string, width int) []string {
	if keys == "" {
		return wrapModalText(back, width)
	}
	hint := keys + " · " + back
	if ansi.StringWidth(hint) > width {
		hint = keys + "\n" + back
	}
	return wrapModalText(hint, width)
}

type modalListRow struct {
	heading string
	option  action
}

type modalListLayout struct {
	rows                   []modalListRow
	top, visible, selected int
	hints, help, notices   []string
}

func (m *Model) keepModalSelectionVisible(page *modalPage, options []action) {
	page.top = m.modalList(page, options, max(1, m.dialogWidth()-2*horizontalPadding), max(1, m.dialogHeight()-2)).top
}

// Reserve space for the selected row and keyboard hints before allocating
// space for contextual help and the rest of the list.
func (m *Model) modalList(page *modalPage, options []action, width, room int) modalListLayout {
	layout := modalListLayout{}
	back, keys := modalTextBack, ""
	if page.kind == modalCommands {
		back = "Esc Close" // Empty Commands is the root, not a back action.
	}
	description, warning := "", ""
	if len(options) == 0 {
		description = "No matching " + page.kind + "."
	} else {
		choice := page.selection(options)
		group := ""
		for i, option := range options {
			if page.kind == modalSettings && group != option.category {
				if group != "" {
					layout.rows = append(layout.rows, modalListRow{})
				}
				group = option.category
				layout.rows = append(layout.rows, modalListRow{heading: group})
			}
			if i == choice {
				layout.selected = len(layout.rows)
			}
			layout.rows = append(layout.rows, modalListRow{option: option})
		}
		option := options[choice]
		description, warning = option.description, option.disabled
		keys = "↑↓ Select"
		switch page.kind {
		case modalSettings:
			switch option.id {
			case settingTheme:
				keys += " · ←→ Cycle · Enter Pick"
			case settingColorMode:
				keys += " · ←→ Cycle"
			case settingMax, settingInterval:
				keys += " · ←→ Adjust · Enter Edit"
			default:
				keys += " · ←→ Toggle"
			}
		default:
			if option.disabled == "" {
				keys += " · Enter Select"
			}
		}
	}
	layout.hints = modalHints(keys, back, width)
	available := room - 2 - len(layout.hints) // title and input
	if room >= 9 {
		available -= 3 // gaps around title, list and footer
	}
	minList := 1
	if len(options) == 0 {
		minList = 0
	}
	helpRoom := max(0, min(4, available-minList))
	noticeRoom := helpRoom
	if page.kind == modalSettings {
		warning, noticeRoom = m.settings.Warning, min(2, helpRoom)
	}
	layout.notices = fitModalText(warning, width-2, noticeRoom)
	layout.help = fitModalText(description, width, helpRoom-len(layout.notices))
	if page.kind == modalSettings && len(options) > 0 && len(layout.help) > 0 &&
		available-len(layout.help)-len(layout.notices) > minList {
		layout.help = append(layout.help, "") // Separate the description from the key legend.
	}
	layout.visible = max(minList, available-len(layout.help)-len(layout.notices))
	if page.kind != modalSettings {
		layout.visible = min(12, layout.visible)
	}
	layout.top = max(0, min(page.top, layout.selected, len(layout.rows)-layout.visible))
	layout.top = max(layout.top, layout.selected-layout.visible+1)
	return layout
}

func fitModalText(text string, width, limit int) []string {
	if limit <= 0 {
		return nil
	}
	rows := wrapModalText(text, width)
	if len(rows) > limit {
		rows = rows[:limit]
		rows[limit-1] = ansi.Truncate(rows[limit-1]+"…", width, "…")
	}
	return rows
}

func modalOptionRow(kind string, a action, selected bool, width int, c palette.Colors) string {
	bg, marker := c.Panel, " "
	if selected {
		bg, marker = c.Selection, "▎"
	}
	fg, markerFG := c.Text, c.Accent
	if a.disabled != "" {
		fg, markerFG = c.Muted, c.Muted
	}
	prefix := line(marker, 1, markerFG, bg, false) + line("", horizontalPadding, fg, bg, false)
	width = max(0, width-1-horizontalPadding)
	title := safeLine(a.title)
	if kind == modalSettings {
		left, right := "  ", "  "
		if selected {
			left, right = "‹ ", " ›"
		}
		// Short values always fit. Give long theme names the remaining space
		// after the label and controls, rather than letting them hide the row.
		value := ansi.Truncate(safeLine(a.value), max(0, width-min(ansi.StringWidth(title), width/2)-6), "…")
		fgValue := c.Muted
		if selected {
			fgValue = c.Text
		}
		return prefix + line(title, max(0, width-4-ansi.StringWidth(value)), fg, bg, false) +
			line(left, 2, c.Accent, bg, false) + line(value, ansi.StringWidth(value), fgValue, bg, false) +
			line(right, 2, c.Accent, bg, false)
	}
	suffix := a.category
	if a.value != "" {
		suffix = a.value
		if a.category != "" {
			suffix += " · " + a.category
		}
	}
	if a.shortcut != "" {
		suffix = a.shortcut + " · " + suffix
	}
	return prefix + columns(title, safeLine(suffix), width, max(1, width*45/100), fg, c.Muted, bg)
}
