package ui

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
)

func TestMergeIndentationComposesWithRelationsAndNarrowWidths(t *testing.T) {
	m, _ := setup(t, 3)
	m.rows[0].Subject = "Merge"
	m.rows[1].Subject, m.rows[1].Author = "Member", "Member Author"
	m.rows[1].MergeDepth, m.rows[1].Relation = 2, gitrepo.LocalOnly
	m.rows[2].Subject = "Trunk"
	m.snapshot.Diverged = true
	m.selected = m.rows[1].SHA
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		rows := strings.Split(m.pane("history", 70, 12, c), "\n")
		baseline := 4 // Two-cell edge gutter plus two-cell content padding.
		for i, expected := range []struct {
			text  string
			depth int
		}{{"Merge", 0}, {"Member", 6}, {"Trunk", 0}} {
			subject := ansi.Strip(rows[3+i*2])
			metadata := ansi.Strip(rows[4+i*2])
			subjectStart, metaStart := strings.Index(subject, expected.text), strings.Index(metadata, m.rows[i].SHA[:8])
			if subjectStart < 0 || metaStart < 0 ||
				ansi.StringWidth(subject[:subjectStart]) != baseline+expected.depth ||
				ansi.StringWidth(metadata[:metaStart]) != baseline+expected.depth {
				t.Fatalf("two-line merge hierarchy lost: %q / %q", subject, metadata)
			}
		}
		if !strings.Contains(rows[5], coloredPrefix("▎", c.Accent, c.Selection)) {
			t.Fatal("indentation moved or removed the fixed selection marker")
		}
	}

	// Deep nesting cannot consume the subject/SHA budget or overflow the pane.
	m.rows = m.rows[1:2]
	m.rows[0].MergeDepth = 100000
	m.rows[0].Subject = "Member " + strings.Repeat("日本語👩🏽‍💻é", 30) + "\x1b[2J"
	m.rows[0].Author = strings.Repeat("作者👩🏽‍💻", 30) + "\x1b[2J"
	m.rows[0].Labels = []string{"tag: " + strings.Repeat("日本語", 30)}
	for _, width := range []int{70, 34, 24, 12, 9, 8, 5, 4, 3, 2, 1, 0} {
		rows := strings.Split(m.pane("history", width, 8, m.colors()), "\n")
		for _, row := range rows {
			if ansi.StringWidth(row) != width || strings.Contains(row, "\x1b[2J") {
				t.Fatalf("width %d: commit row overflow: %q", width, ansi.Strip(row))
			}
		}
		if width >= 24 && !strings.Contains(ansi.Strip(rows[3]), "M") {
			t.Fatalf("width %d: indentation hid subject: %q", width, ansi.Strip(rows[3]))
		}
		if width >= 5 && !strings.HasSuffix(ansi.Strip(rows[3]), "↑ ") {
			t.Fatalf("width %d: long Unicode content moved the relation gutter", width)
		}
		if width >= 9 && width <= 12 && !strings.HasPrefix(ansi.Strip(rows[3]), "▎   …") {
			t.Fatalf("width %d: right metadata hid all subject content", width)
		}
	}
}

func TestCommitRelationGuttersStayNeutralAndFixed(t *testing.T) {
	m, _ := setup(t, 3)
	for _, mode := range []string{"dark", "light"} {
		m.settings.Value.ColorMode = mode
		c := m.colors()
		for _, tc := range []struct {
			name     string
			relation gitrepo.CommitRelation
			diverged bool
			arrow    string
		}{
			{"ahead only", gitrepo.LocalOnly, false, "↑"},
			{"behind only", gitrepo.Incoming, false, "↓"},
			{"diverged local", gitrepo.LocalOnly, true, "↑"},
			{"diverged incoming", gitrepo.Incoming, true, "↓"},
			{"shared or unrelated", gitrepo.Unmarked, true, " "},
			{"no upstream", gitrepo.Unmarked, false, " "},
		} {
			m.snapshot.Diverged = tc.diverged
			for i := range m.rows {
				m.rows[i].Relation = tc.relation
				m.rows[i].MergeDepth = i * 2
				m.rows[i].Subject = strings.Repeat("日本語👩🏽‍💻é", 30) + "\x1b[2J"
				m.rows[i].Author = strings.Repeat("作者", 30)
				m.rows[i].Labels = []string{"HEAD", "tag: " + strings.Repeat("標籤", 30)}
			}
			m.selected = m.rows[1].SHA
			raw := m.pane("history", 70, 12, c)
			screen := uv.NewStyledString(raw).Lines(ansi.GraphemeWidth)
			if !strings.HasPrefix(ansi.Strip(strings.Split(raw, "\n")[1]), "   History ") {
				t.Fatal("list header lost its two-cell inset")
			}
			for i := range m.rows {
				bg := m.paneBackground("history", c)
				if i == 1 {
					bg = c.Selection
				}
				for dy := range 2 {
					row := screen[3+2*i+dy]
					if got := ansi.StringWidth(row.String()); got != 70 {
						t.Fatalf("%s/%s: row width %d", mode, tc.name, got)
					}
					wantArrow := tc.arrow
					if dy == 1 {
						wantArrow = " "
					}
					if row[len(row)-2].Content != wantArrow || row[len(row)-1].Content != " " {
						t.Fatalf("%s/%s: relation moved or repeated on metadata line", mode, tc.name)
					}
					if actual := row[len(row)-2].Style.Fg; actual == nil ||
						color.RGBAModel.Convert(actual) != color.RGBAModel.Convert(lipgloss.Color(c.Muted)) {
						t.Fatalf("%s/%s: relation must remain neutral, including selection", mode, tc.name)
					}
					for x, cell := range row {
						if cell.Style.Bg == nil ||
							color.RGBAModel.Convert(cell.Style.Bg) != color.RGBAModel.Convert(lipgloss.Color(bg)) {
							t.Fatalf("%s/%s: line %d cell %d lost full-width surface", mode, tc.name, dy, x)
						}
					}
					if i == 1 && row[0].Content != "▎" {
						t.Fatal("hierarchy moved the selection marker out of the leading gutter")
					}
				}
			}
		}
	}
}

func TestMergeDepthSurvivesPageDeliveryRefreshAndNavigation(t *testing.T) {
	m, repo := setup(t, 205)
	m.settings.Value.MaxCommits = 400
	repo.rows[199].MergeDepth = 1
	repo.rows[200].MergeDepth = 2
	repo.rows[201].MergeDepth = 0
	deliverRefresh(t, m, m.refresh(m.scope))
	m.selectCommit(m.rows[199].SHA)
	cmd := press(m, tea.KeyDown, "")
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("expected paged navigation")
	}
	for _, command := range batch {
		if msg, ok := command().(pageMsg); ok {
			m.Update(msg)
		}
	}
	if m.selected != repo.rows[200].SHA || m.rows[200].MergeDepth != 2 {
		t.Fatal("page boundary lost nested membership or navigation")
	}
	press(m, tea.KeyDown, "")
	if m.selected != repo.rows[201].SHA || m.rows[201].MergeDepth != 0 {
		t.Fatal("navigation did not restore trunk level")
	}
	// A refresh replaces ancestry rather than retaining the old presentation.
	repo.rows[200].MergeDepth = 1
	deliverRefresh(t, m, m.refresh(m.scope))
	if m.selected != repo.rows[201].SHA || m.rows[200].MergeDepth != 1 {
		t.Fatal("refresh lost selection or retained stale membership")
	}
}

func TestUnavailableMergeAncestryRemainsVisibleInDiagnostics(t *testing.T) {
	m, repo := setup(t, 1)
	warning := "Merge indentation unavailable: incomplete commit ancestry."
	repo.snapshot = &gitrepo.Snapshot{AncestryWarning: warning}
	deliverRefresh(t, m, m.refresh(m.scope))
	if !strings.Contains(m.warnings(), warning) || m.notice != warning || len(m.rows) != 1 {
		t.Fatal("incomplete ancestry must warn without hiding history")
	}
	m.Update(noticeExpiredMsg(m.noticeID))
	if m.notice != "" || !strings.Contains(m.warnings(), warning) {
		t.Fatal("ancestry limitation disappeared with the transient notice")
	}
	repo.snapshot = &gitrepo.Snapshot{}
	deliverRefresh(t, m, m.refresh(m.scope))
	if strings.Contains(m.warnings(), warning) {
		t.Fatal("refresh retained an obsolete ancestry limitation")
	}
}
