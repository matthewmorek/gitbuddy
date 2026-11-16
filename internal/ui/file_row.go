package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/palette"
)

// fileRow is the shared changed-file presentation contract for live files and
// commit details: index/worktree status + path on the left, counts on the right.
// Commit rename scores retain their full status token (e.g. R100).
type fileRow struct {
	path, oldPath, index, worktree string
	stats                          gitrepo.LineStats
	untracked                      bool
}

func (m *Model) fileStatusColor(code string, c palette.Colors) string {
	switch {
	case strings.HasPrefix(code, "A"):
		return m.semanticColor(c, "#4ade80", "#15803d")
	case strings.HasPrefix(code, "D"), code == "U":
		return errorColor(c)
	case strings.HasPrefix(code, "M"), code == "T":
		return m.semanticColor(c, "#60a5fa", "#2563eb")
	case strings.HasPrefix(code, "R"), strings.HasPrefix(code, "C"):
		return c.Accent
	default:
		return c.Muted
	}
}

func (m *Model) renderFileRow(f fileRow, width int, c palette.Colors, bg string) string {
	index, worktree := f.index, f.worktree
	if index == "" || index == "." {
		index = " "
	}
	if worktree == "" || worktree == "." {
		worktree = " "
	}
	if f.untracked {
		worktree = "?"
	}
	fg := c.Text
	// A recreated file with a staged deletion is not wholly untracked.
	if f.untracked && index == " " {
		fg = c.Muted
	}
	counts := ""
	switch {
	case !f.stats.Known:
		counts = "unavailable"
	case f.stats.Binary:
		counts = "binary"
	default:
		for _, count := range []struct {
			n     int
			sign  string
			color string
		}{{f.stats.Added, "+", m.semanticColor(c, "#4ade80", "#15803d")}, {f.stats.Deleted, "-", errorColor(c)}} {
			if count.n == 0 {
				continue
			}
			color := count.color
			if fg == c.Muted {
				color = c.Muted
			}
			text := fmt.Sprintf("%s%d", count.sign, count.n)
			if counts != "" {
				counts += " "
			}
			counts += line(text, len(text), color, bg, false)
		}
	}
	status := line(safeLine(index), max(1, ansi.StringWidth(safeLine(index))), m.fileStatusColor(index, c), bg, false) +
		line(safeLine(worktree), 1, m.fileStatusColor(worktree, c), bg, false) + " "
	statusWidth := min(width, ansi.StringWidth(status))
	countWidth := min(ansi.StringWidth(counts), max(0, width-statusWidth-1-horizontalPadding))
	gap := 0
	if countWidth > 0 {
		gap = horizontalPadding
	}
	pathWidth := max(0, width-statusWidth-countWidth-gap)
	return line(status, statusWidth, fg, bg, false) +
		line(changedFileName(f.path, f.oldPath, pathWidth), pathWidth, fg, bg, false) +
		line("", gap, fg, bg, false) + line(counts, countWidth, c.Muted, bg, false)
}
