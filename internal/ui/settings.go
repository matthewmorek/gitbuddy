package ui

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/matthewmorek/gitbuddy/internal/preferences"
)

const (
	settingTheme     = "themes"
	settingColorMode = "modes"
	settingAutoFetch = "auto"
	settingInterval  = "interval"
	settingAutoStash = "auto-stash"
	settingMax       = "max"
)

func (m *Model) settingRows() []action {
	s := m.settings.Value
	return []action{
		{id: settingTheme, title: "Theme", category: "Appearance", value: s.Theme,
			description: "Choose the colors Gitbuddy uses.", keywords: "palette",
			child: modalThemes, childSelection: s.Theme},
		{id: settingColorMode, title: "Color mode", category: "Appearance", value: s.ColorMode,
			description: "Follow the terminal (system), or force light or dark mode.", keywords: "colour"},
		{id: settingAutoFetch, title: "Automatic fetch enabled", category: "Fetch", value: onOff(s.AutoFetch),
			description: "Fetch remotes at startup and periodically in the background.", keywords: "background"},
		{id: settingInterval, title: "Automatic fetch interval", category: "Fetch", value: fmt.Sprintf("%d min", s.FetchIntervalMinutes),
			description: "Set the time between background fetches.", keywords: "minutes schedule"},
		{id: settingAutoStash, title: "Automatic stash on Safe pull", category: "Pull", value: onOff(s.AutoStashOnSafePull),
			description: "Temporarily stash local changes during Safe pull.", keywords: "dirty working tree"},
		{id: settingMax, title: "Maximum commits", category: "History", value: strconv.Itoa(s.MaxCommits),
			description: "Limit how many commits Gitbuddy loads for the active history scope.", keywords: "limit log"},
	}
}

func onOff(value bool) string {
	if value {
		return "On"
	}
	return "Off"
}

func cycleSetting(values []string, current string, delta int) string {
	if len(values) == 0 {
		return current
	}
	at := slices.Index(values, current)
	if at < 0 && delta < 0 {
		at = 0
	}
	return values[(at+delta+len(values))%len(values)]
}

func (m *Model) adjustSetting(id string, delta int) tea.Cmd {
	next := m.settings.Value
	switch id {
	case settingTheme:
		next.Theme = cycleSetting(m.themes.Names, next.Theme, delta)
	case settingColorMode:
		next.ColorMode = cycleSetting([]string{"system", "light", "dark"}, next.ColorMode, delta)
	case settingAutoFetch:
		next.AutoFetch = !next.AutoFetch
	case settingAutoStash:
		next.AutoStashOnSafePull = !next.AutoStashOnSafePull
	case settingMax, settingInterval:
		number := numericSetting(&next, id)
		if *number == math.MaxInt && delta > 0 {
			return nil
		}
		*number += delta
	}
	if next == m.settings.Value || preferences.Validate(next) != nil {
		return nil
	}
	return m.save(next)
}

func numericSetting(s *preferences.Settings, id string) *int {
	if id == settingInterval {
		return &s.FetchIntervalMinutes
	}
	return &s.MaxCommits
}

func (m *Model) editSetting(id string) tea.Cmd {
	switch id {
	case settingMax, settingInterval:
		return m.modals.push(modalPage{kind: modalNumber, setting: id}, strconv.Itoa(*numericSetting(&m.settings.Value, id)))
	}
	return nil
}

func (m *Model) commitNumber(page *modalPage) tea.Cmd {
	n, err := strconv.Atoi(strings.TrimSpace(page.input.Value()))
	if err != nil {
		page.validation = "Enter a positive whole number within the range below."
		return nil
	}
	next := m.settings.Value
	*numericSetting(&next, page.setting) = n
	if preferences.Validate(next) != nil {
		page.validation = "That value is outside the allowed range."
		return nil
	}
	save := m.save(next)
	return tea.Batch(save, m.modals.pop())
}

func (m *Model) save(next preferences.Settings) tea.Cmd {
	old := m.settings.Value
	err := m.settings.Update(next)
	m.fail(err)
	if err != nil && m.settings.Value == old {
		return nil
	}
	cmds := []tea.Cmd{m.schedule()}
	if next.MaxCommits != old.MaxCommits {
		if len(m.rows) > next.MaxCommits {
			m.rows = m.rows[:next.MaxCommits]
		}
		m.top = min(m.top, max(0, len(m.rows)-m.visibleRows()))
		cmds = append(cmds, m.refresh(m.activeScope()), m.spinner.Tick)
	}
	return tea.Batch(cmds...)
}
