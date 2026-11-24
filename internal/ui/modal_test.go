package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/matthewmorek/gitbuddy/internal/palette"
	"github.com/matthewmorek/gitbuddy/internal/preferences"
)

// Existing tests can seed a root directly when opening it is not the contract
// under test. Navigation tests send keys through Update instead.
func (m *Model) openDialog(kind, value string) {
	m.modals.dismiss()
	m.modals.push(modalPage{kind: kind}, value)
}

func (m *Model) modalKind() string {
	if page := m.modals.current(); page != nil {
		return page.kind
	}
	return ""
}

func (m *Model) modalInput() string {
	if page := m.modals.current(); page != nil && page.input != nil {
		return page.input.Value()
	}
	return ""
}

func modalCtrl(m *Model, key rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: key, Mod: tea.ModCtrl})
	return cmd
}

func modalType(m *Model, text string) {
	for _, r := range text {
		press(m, r, string(r))
	}
}

func searchCommand(m *Model, query string) {
	modalCtrl(m, 'p')
	modalType(m, query)
}

func eraseModalText(m *Model) {
	for range m.modalInput() {
		press(m, tea.KeyBackspace, "")
	}
}

func TestOpenSettingsFromCommandPalette(t *testing.T) {
	m, _ := setup(t, 1)
	searchCommand(m, "open settings")
	press(m, tea.KeyEnter, "")
	view := plain(m)
	if !strings.Contains(view, "Theme") || strings.Contains(view, "Open settings") {
		t.Fatalf("Settings should show settings, not its own opener:\n%s", view)
	}
	var ids []string
	for _, row := range m.options() {
		ids = append(ids, row.id)
	}
	if !slices.Equal(ids, []string{settingTheme, settingColorMode, settingAutoFetch, settingInterval, settingAutoStash, settingMax}) {
		t.Fatalf("expected only the six settings, got %v", ids)
	}
}

func TestSettingsNumericEditorRestoresSearchSelectionAndFocus(t *testing.T) {
	m, _ := setup(t, 1)
	m.focus, m.started = "details", true
	selected := m.selected
	searchCommand(m, "open settings")
	press(m, tea.KeyEnter, "")
	modalType(m, "fetch")
	press(m, tea.KeyDown, "")
	before := m.settings.Value
	timer := m.timer
	press(m, tea.KeyEnter, "")
	if m.modalKind() != modalNumber || m.modalInput() != "5" {
		t.Fatal("selected fetch interval did not open a prefilled editor")
	}
	press(m, tea.KeyBackspace, "")
	if m.modalKind() != modalNumber || m.modalInput() != "" {
		t.Fatal("deleting the last character should not leave the editor")
	}
	modalType(m, "1441")
	press(m, tea.KeyEnter, "")
	if m.modalKind() != modalNumber || m.settings.Value != before || m.timer != timer ||
		!strings.Contains(plain(m), "outside the allowed range") || m.failure != "" {
		t.Fatal("out-of-range edit was not rejected inline without changing settings")
	}
	eraseModalText(m)
	press(m, tea.KeyBackspace, "")
	if m.modalKind() != modalSettings || m.modalInput() != "fetch" || m.settings.Value != before || m.timer != timer {
		t.Fatal("Backspace did not restore the filtered parent without saving")
	}
	press(m, tea.KeyEnter, "")
	if m.modalKind() != modalNumber || m.modalInput() != "5" {
		t.Fatal("parent lost the interval selection")
	}
	eraseModalText(m)
	modalType(m, "1440")
	press(m, tea.KeyEnter, "")
	if m.modalKind() != modalSettings || m.modalInput() != "fetch" ||
		m.settings.Value.FetchIntervalMinutes != 1440 || m.timer <= timer ||
		preferences.Load(m.settings.Directory).Value.FetchIntervalMinutes != 1440 {
		t.Fatal("valid edit did not persist, reschedule, and restore the Settings page")
	}
	timer = m.timer
	press(m, tea.KeyRight, "")
	if m.timer != timer || m.settings.Value.FetchIntervalMinutes != 1440 {
		t.Fatal("step past the interval limit changed the scheduler or value")
	}
	press(m, tea.KeyLeft, "")
	if preferences.Load(m.settings.Directory).Value.FetchIntervalMinutes != 1439 {
		t.Fatal("Left did not persist a one-minute change on the restored row")
	}
	eraseModalText(m)
	press(m, tea.KeyBackspace, "")
	if m.modalKind() != modalCommands || m.modalInput() != "open settings" {
		t.Fatal("Settings did not return to its Commands parent")
	}
	eraseModalText(m)
	press(m, tea.KeyBackspace, "")
	if m.modalKind() != modalCommands || m.modalInput() != "" {
		t.Fatal("Backspace paged back from the empty Commands root")
	}
	modalType(m, "open settings")
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnter, "") // Theme picker
	press(m, tea.KeyEscape, "")
	if m.modalKind() != "" || m.focus != "details" || m.selected != selected {
		t.Fatal("Escape did not dismiss the entire tree and restore underlying navigation")
	}
}

func TestSettingsDirectEntryStepsAndPreservesExternalConfig(t *testing.T) {
	m, _ := setup(t, 3)
	searchCommand(m, "appearance")
	press(m, tea.KeyDown, "") // Color mode is the second Appearance result.
	press(m, tea.KeyLeft, "") // Save the Commands search cursor, not just text.
	press(m, tea.KeyEnter, "")
	if m.modalKind() != modalSettings || m.modalInput() != "" || len(m.options()) != 6 {
		t.Fatal("an individual setting did not open the unfiltered Settings page")
	}
	press(m, tea.KeyLeft, "")
	if preferences.Load(m.settings.Directory).Value.ColorMode != "dark" {
		t.Fatal("Color mode did not wrap backward from system to dark")
	}
	press(m, tea.KeyRight, "")
	if preferences.Load(m.settings.Directory).Value.ColorMode != "system" {
		t.Fatal("Color mode did not wrap forward back to system")
	}
	press(m, tea.KeyBackspace, "")
	commands := m.options()
	if m.modalKind() != modalCommands || m.modalInput() != "appearance" ||
		commands[m.modals.current().selection(commands)].id != settingColorMode {
		t.Fatal("Commands lost the query or selected action")
	}
	modalType(m, "x")
	if m.modalInput() != "appearancxe" {
		t.Fatalf("Commands lost cursor position: %q", m.modalInput())
	}
	modalCtrl(m, 'p') // Existing toggle dismisses rather than reopening Commands.
	if m.modalKind() != "" {
		t.Fatal("Ctrl+P did not dismiss the modal tree")
	}
	searchCommand(m, "limit log")
	press(m, tea.KeyEnter, "")
	generation := m.generation
	press(m, tea.KeyLeft, "")
	if preferences.Load(m.settings.Directory).Value.MaxCommits != 99 || m.generation <= generation {
		t.Fatal("history limit did not persist and refresh on a one-step edit")
	}
	press(m, tea.KeyEscape, "")

	file := filepath.Join(m.settings.Directory, "config.json")
	external := `{"autoFetch":false,"maxCommits":32}`
	if err := os.WriteFile(file, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	searchCommand(m, "automatic fetch enabled")
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyRight, "")
	saved, err := os.ReadFile(file)
	if err != nil || string(saved) != external || !m.settings.Value.AutoFetch ||
		!strings.Contains(plain(m), "for this session") {
		t.Fatalf("save failure did not retain the session value, warn inline and preserve external edits: %s, %v", saved, err)
	}
	modalCtrl(m, 'c')
	if m.modalKind() != "" || m.closing {
		t.Fatal("Ctrl+C quit instead of dismissing Settings")
	}
}

func TestModalBackFromDiagnosticsAndStashNote(t *testing.T) {
	m, repo := setup(t, 1)
	searchCommand(m, "stash all")
	press(m, tea.KeyEnter, "")
	if m.modalKind() != modalCommands || m.failure != "Nothing to stash" {
		t.Fatal("disabled Stash all should report why without opening its child")
	}
	press(m, tea.KeyEscape, "")

	m.status.Dirty = true
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	modalCtrl(m, 'p')
	options := m.options()
	for _, option := range options {
		if option.id == modalErrors {
			break
		}
		press(m, tea.KeyDown, "")
	}
	panel := func() string { return ansi.Strip(m.dialogView(m.colors(), m.dialogWidth(), m.dialogHeight())) }
	commands := panel()
	press(m, tea.KeyEnter, "")
	if m.modalKind() != modalErrors {
		t.Fatal("did not select diagnostics in the scrolled command list")
	}
	press(m, tea.KeyBackspace, "")
	if m.modalKind() != modalCommands || commands != panel() {
		t.Fatal("returning from diagnostics changed the command viewport")
	}
	press(m, tea.KeyEscape, "")

	searchCommand(m, "show errors and warnings")
	press(m, tea.KeyEnter, "")
	if m.modalKind() != modalErrors {
		t.Fatal("did not open diagnostics")
	}
	press(m, tea.KeyBackspace, "")
	if m.modalKind() != modalCommands || m.modalInput() != "show errors and warnings" {
		t.Fatal("Backspace did not immediately return from non-text diagnostics")
	}
	press(m, tea.KeyEscape, "")

	searchCommand(m, "stash all")
	press(m, tea.KeyEnter, "")
	modalType(m, "x")
	press(m, tea.KeyBackspace, "")
	if m.modalKind() != modalStashNote || m.modalInput() != "" {
		t.Fatal("Backspace on the last note character should keep the note open")
	}
	press(m, tea.KeyBackspace, "")
	if m.modalKind() != modalCommands || m.modalInput() != "stash all" ||
		m.operation != "" || len(repo.stashed) != 0 {
		t.Fatal("Backing out of the stash note lost the parent or started a mutation")
	}
	press(m, tea.KeyEnter, "")
	m.operation = "Fetching"
	press(m, tea.KeyEnter, "")
	if m.modalKind() != modalStashNote || m.operation != "Fetching" || len(repo.stashed) != 0 {
		t.Fatal("stash note failed to recheck the mutation guard on submit")
	}
}

func TestCompactModalKeepsSelectedValueAndHelpVisible(t *testing.T) {
	m, _ := setup(t, 1)
	customTheme := "custom-palette-" + strings.Repeat("very-long-", 10) + "end"
	dir := filepath.Join(m.settings.Directory, "themes")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]palette.Colors{"dark": m.themes.Resolve("opencode", "dark")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, customTheme+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	m.themes = palette.Load(m.settings.Directory)
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 60, Height: 18}} {
		m.Update(size)
		searchCommand(m, "settings theme")
		press(m, tea.KeyEnter, "")
		if size.Width == 80 {
			press(m, tea.KeyLeft, "")
			if m.settings.Value.Theme != customTheme {
				t.Fatal("theme cycling did not wrap to the custom catalog entry")
			}
			press(m, tea.KeyRight, "")
		}
		press(m, tea.KeyEnter, "")
		if m.modalKind() != modalThemes {
			t.Fatal("Settings did not open the Theme picker")
		}
		if size.Width == 80 {
			modalType(m, "custom palette end")
		}
		// At the second size, Enter without filtering should retain the current custom theme.
		press(m, tea.KeyEnter, "")
		panel := func() string { return ansi.Strip(m.dialogView(m.colors(), m.dialogWidth(), m.dialogHeight())) }
		view := panel()
		t.Logf("%dx%d Settings:\n%s", size.Width, size.Height, view)
		if m.settings.Value.Theme != customTheme || !strings.Contains(view, "▎  Theme") ||
			!strings.Contains(view, "‹ custom-palette-") || !strings.Contains(view, "… ›") ||
			strings.Count(view, "‹ ") != 1 || !strings.Contains(view, "Choose the colors") ||
			!strings.Contains(view, "Enter Pick") || !strings.Contains(view, "Esc Close") {
			t.Fatal("compact Settings lost the selected theme, value, controls or help")
		}
		for range 5 {
			press(m, tea.KeyDown, "")
		}
		view = panel()
		if !strings.Contains(view, "▎  Maximum commits") || !strings.Contains(view, "‹ 100 ›") ||
			!strings.Contains(view, "Limit how many commits") || !strings.Contains(view, "Enter Edit") {
			t.Fatal("scrolling Settings hid the selected history limit, value or help")
		}
		press(m, tea.KeyEscape, "")
		m.status.Dirty = true
		searchCommand(m, "fast-forward only")
		view = panel()
		t.Logf("%dx%d disabled Commands:\n%s", size.Width, size.Height, view)
		flat := strings.Join(strings.Fields(strings.ReplaceAll(view, "▎", "")), " ")
		reason := "Working tree must be clean, including untracked files"
		if !strings.Contains(view, "▎  Pull") || !strings.Contains(flat, reason) ||
			strings.Index(flat, reason) > strings.Index(flat, "Pull without creating") ||
			strings.Contains(view, "Enter Select") || !strings.Contains(view, "Esc Close") {
			t.Fatal("disabled action lost its reason, description or valid shortcuts")
		}
		for _, row := range strings.Split(plain(m), "\n") {
			if ansi.StringWidth(row) > size.Width {
				t.Fatalf("frame exceeds %d columns: %q", size.Width, row)
			}
		}
		press(m, tea.KeyEscape, "")
	}
}
