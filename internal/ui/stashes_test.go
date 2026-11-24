package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/preferences"
)

func gitActionResult[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var find func(tea.Cmd) (T, bool)
	find = func(cmd tea.Cmd) (T, bool) {
		var zero T
		if cmd == nil {
			return zero, false
		}
		msg := cmd()
		if result, ok := msg.(T); ok {
			return result, true
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, child := range batch {
				if result, ok := find(child); ok {
					return result, true
				}
			}
		}
		return zero, false
	}
	if result, ok := find(cmd); ok {
		return result
	}
	var zero T
	t.Fatalf("missing %T", zero)
	return zero
}

func TestSafePullPalettePreferenceAndMutationLifecycle(t *testing.T) {
	m, repo := setup(t, 3)
	m.status.Dirty, repo.status.Dirty = true, true
	m.openDialog("commands", "pull")
	for _, a := range m.options() {
		switch a.id {
		case "safe-pull":
			if a.disabled != "" {
				t.Fatal(a.disabled)
			}
		case "ff-only":
			if a.disabled == "" {
				t.Fatal("legacy pull enabled on a dirty checkout")
			}
		}
	}
	m.modals.dismiss()
	m.openDialog("settings", "automatic stash")
	press(m, tea.KeyRight, "")
	if preferences.Load(m.settings.Directory).Value.AutoStashOnSafePull {
		t.Fatal("Safe pull preference did not persist")
	}
	m.openDialog("commands", "safe pull")
	if cmd := press(m, tea.KeyEnter, ""); m.operation != "" || len(repo.safePulls) != 0 {
		t.Fatalf("dirty clean-only Safe pull started: %v", cmd != nil)
	}
	m.modals.dismiss()
	m.status.Dirty, repo.status.Dirty = false, false
	m.openDialog("commands", "safe pull")
	result := gitActionResult[mutationMsg](t, press(m, tea.KeyEnter, ""))
	if !slices.Equal(repo.safePulls, []bool{false}) || m.mutate("fetch") != nil || m.mutate("ff-only") != nil {
		t.Fatal("Safe pull did not use preference and shared serialization")
	}
	if _, cmd := m.Update(QuitMsg{}); cmd != nil {
		t.Fatal("quit interrupted the in-flight mutation")
	}
	if _, cmd := m.Update(result); cmd == nil {
		t.Fatal("finished mutation did not release pending quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("quit requested something other than orderly shutdown")
	}
}

func TestStashesViewAndConfirmationFreezeEntry(t *testing.T) {
	m, repo := setup(t, 3)
	open := func() tea.Cmd {
		m.openDialog("commands", "stashes")
		return press(m, tea.KeyEnter, "")
	}
	cmd := open()
	if !strings.Contains(plain(m), "Loading stashes") {
		t.Fatal("stash loading is not visible")
	}
	old := gitActionResult[stashesMsg](t, cmd)
	press(m, '1', "1")
	repo.stashErr = errors.New("Cannot read fixture stash log")
	msg := gitActionResult[stashesMsg](t, press(m, '3', "3"))
	m.Update(old)
	m.Update(msg)
	if !strings.Contains(plain(m), "Cannot read fixture stash log") || press(m, 'a', "a") != nil || m.modalKind() != "" {
		t.Fatal("stash read error is missing or actionable")
	}
	repo.stashErr = nil
	m.Update(gitActionResult[stashesMsg](t, m.refreshStashes()))
	if !strings.Contains(plain(m), "No stashes") || press(m, 'd', "d") != nil || m.modalKind() != "" {
		t.Fatal("empty stash list must not apply anything")
	}
	date := time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local)
	prev := gitrepo.Stash{ID: strings.Repeat("b", 40), Subject: "prior backup", Date: date.AddDate(0, 0, -1)}
	latest := gitrepo.Stash{ID: strings.Repeat("a", 40), Subject: "latest backup", Date: date}
	repo.stashRows = []gitrepo.Stash{latest, prev}
	repo.stashPreviews = map[string]gitrepo.StashPreview{
		latest.ID: {Tracked: []gitrepo.ChangedFile{{Path: "stale.txt", Status: "A", Added: 1}}},
		prev.ID: {
			Tracked:   []gitrepo.ChangedFile{{Path: "new.txt", OldPath: "old.txt", Status: "R100"}},
			Untracked: []gitrepo.ChangedFile{{Path: "loose.bin", Status: "A", Binary: true}},
			Index:     []gitrepo.ChangedFile{{Path: "saved.txt", Status: "M", Added: 3, Deleted: 1}},
		},
	}
	_, cmd = m.Update(gitActionResult[stashesMsg](t, press(m, 'r', "r")))
	oldPreview := gitActionResult[stashPreviewMsg](t, cmd)
	frame := plain(m)
	if !strings.Contains(frame, "2026-11-13") || !strings.Contains(frame, "Stash contents") ||
		strings.Index(frame, "latest backup") < 0 || strings.Index(frame, "prior backup") < strings.Index(frame, "latest backup") {
		t.Fatalf("missing dates, ordering or wide preview: %s", frame)
	}
	preview := gitActionResult[stashPreviewMsg](t, press(m, tea.KeyDown, ""))
	m.Update(preview)
	m.Update(oldPreview)
	frame = plain(m)
	for _, want := range []string{"old.txt → new.txt", "R100", "loose.bin", "binary", "Saved index differences", "+3", "-1"} {
		if !strings.Contains(frame, want) || strings.Contains(frame, "stale.txt") {
			t.Fatalf("missing %s or stale preview rendered:\n%s", want, frame)
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 75, Height: 24})
	if strings.Contains(plain(m), "Stash contents") {
		t.Fatal("narrow list unexpectedly displayed preview")
	}
	press(m, tea.KeyEnter, "")
	press(m, 'a', "a")
	press(m, 'd', "d")
	if !strings.Contains(plain(m), "loose.bin") || m.modalKind() != "" {
		t.Fatal("narrow Enter did not open a read-only preview")
	}
	press(m, tea.KeyEscape, "")
	press(m, 'a', "a")
	frame = plain(m)
	if !strings.Contains(frame, "Apply stash?") || !strings.Contains(frame, "prior backup") ||
		!strings.Contains(frame, "2026-11-12") || !strings.Contains(frame, "▎ Cancel") {
		t.Fatalf("confirmation does not identify selection or default to Cancel: %s", frame)
	}
	press(m, tea.KeyEnter, "")
	press(m, 'd', "d")
	if !strings.Contains(plain(m), "Delete stash?") {
		t.Fatal("delete confirmation not visible")
	}
	press(m, tea.KeyRight, "")
	press(m, tea.KeyBackspace, "")
	if len(repo.applied)+len(repo.deleted) != 0 || m.operation != "" || m.modalKind() != "" || m.focus != "history" {
		t.Fatal("Backspace did not safely close the direct confirmation")
	}
	press(m, 'a', "a")
	repo.stashRows = []gitrepo.Stash{latest}
	m.Update(gitActionResult[stashesMsg](t, m.refreshStashes()))
	press(m, tea.KeyRight, "")
	result := gitActionResult[mutationMsg](t, press(m, tea.KeyEnter, ""))
	if len(repo.applied) != 1 || !repo.applied[0].SameEntry(prev) || m.modalKind() != "" {
		t.Fatalf("wrong stash applied: %v", repo.applied)
	}
	generation := m.generation
	_, cmd = m.Update(result)
	if m.generation <= generation || !strings.Contains(plain(m), "Stash applied and kept") {
		t.Fatal("apply did not refresh the repository and show the outcome")
	}
	m.Update(gitActionResult[stashesMsg](t, cmd))
	press(m, 'd', "d")
	press(m, tea.KeyTab, "")
	gitActionResult[mutationMsg](t, press(m, tea.KeyEnter, ""))
	if len(repo.deleted) != 1 || !repo.deleted[0].SameEntry(latest) {
		t.Fatalf("wrong stash deleted: %v", repo.deleted)
	}
}

func TestStashAllCancelAndOptionalNote(t *testing.T) {
	for _, note := range []string{"", "Work in progress"} {
		m, repo := setup(t, 1)
		m.status.Dirty = true
		open := func() {
			m.openDialog("commands", "stash all")
			press(m, tea.KeyEnter, "")
		}
		open()
		if !strings.Contains(plain(m), "Optional note") {
			t.Fatal("stash note prompt not visible")
		}
		press(m, 'x', "x")
		press(m, tea.KeyEscape, "")
		if len(repo.stashed) != 0 || m.operation != "" {
			t.Fatal("cancel mutated the repo")
		}
		open()
		for _, c := range note {
			press(m, c, string(c))
		}
		result := gitActionResult[mutationMsg](t, press(m, tea.KeyEnter, ""))
		m.Update(result)
		if !slices.Equal(repo.stashed, []string{note}) || !strings.Contains(plain(m), "Changes saved in Stashes") {
			t.Fatal("stash did not submit the optional note and report success")
		}
	}
}
