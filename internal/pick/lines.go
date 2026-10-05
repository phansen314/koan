package pick

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/phansen314/ftask/internal/model"
)

// A line is tab-delimited (pick-spec.md, Line fields and Lines): the hidden
// key, then the columns before the tags, the tags, and the title. fzf shows
// fields 2 on (--with-nth) and matches the tags and the title, the second
// and third it shows (--nth, which counts shown fields).
const (
	lineDelimiter = "\t"
	lineWithNth   = "2.."
	lineNth       = "2,3"
)

// capWidth is the most cells the detail and folder columns take.
const capWidth = 24

// SGR sequences for the lines' colors. Each turns off only what it turned
// on, so a muted column inside a dimmed line stays dimmed.
const (
	dimOn    = "\x1b[2m"
	dimOff   = "\x1b[22m"
	mutedOn  = "\x1b[90m"
	mutedOff = "\x1b[39m"
)

// key is a task line's key, <id>@<folder>: two copies of a duplicated ID
// are never in one folder.
func key(v model.TaskView) string {
	return fmt.Sprintf("%d@%s", v.ID, v.Folder)
}

// renderLines renders views, in order, as fzf's lines, without newlines.
// Every column but the title is padded to the widest in views, in terminal
// cells; the detail and folder columns are capped. color is false when
// NO_COLOR is set.
func renderLines(views []model.TaskView, color bool) []string {
	type row struct{ state, id, detail, folder, tags string }
	rows := make([]row, len(views))
	var wState, wID, wDetail, wFolder, wTags int
	for i, v := range views {
		r := row{
			state:  stateMark(v.Readiness),
			id:     strconv.FormatInt(int64(v.ID), 10),
			detail: runewidth.Truncate(detail(v), capWidth, "…"),
			folder: runewidth.Truncate(string(v.Folder), capWidth, "…"),
			tags:   tagList(v.Tags),
		}
		rows[i] = r
		wState = max(wState, runewidth.StringWidth(r.state))
		wID = max(wID, runewidth.StringWidth(r.id))
		wDetail = max(wDetail, runewidth.StringWidth(r.detail))
		wFolder = max(wFolder, runewidth.StringWidth(r.folder))
		wTags = max(wTags, runewidth.StringWidth(r.tags))
	}
	lines := make([]string, len(views))
	for i, v := range views {
		r := rows[i]
		folder := runewidth.FillRight(r.folder, wFolder)
		tags := runewidth.FillRight(r.tags, wTags)
		title := string(v.Title)
		if color {
			folder = mutedOn + folder + mutedOff
			tags = mutedOn + tags + mutedOff
		}
		cols := runewidth.FillRight(r.state, wState) + "  " +
			runewidth.FillLeft(r.id, wID) + "  " +
			runewidth.FillRight(r.detail, wDetail) + "  " +
			folder + " "
		tags += " "
		if color && v.Readiness != model.Ready {
			cols, tags, title = dimOn+cols+dimOff, dimOn+tags+dimOff, dimOn+title+dimOff
		}
		lines[i] = strings.Join([]string{key(v), cols, tags, title}, lineDelimiter)
	}
	return lines
}

func stateMark(r model.Readiness) string {
	switch r {
	case model.Ready:
		return "●"
	case model.Blocked:
		return "◐"
	}
	return "✓"
}

// detail is a blocked task's blocking IDs, after →, or the priority, after
// p; "" for neither.
func detail(v model.TaskView) string {
	if v.Readiness == model.Blocked {
		ids := make([]string, len(v.Blocking))
		for i, id := range v.Blocking {
			ids[i] = strconv.FormatInt(int64(id), 10)
		}
		return "→" + strings.Join(ids, ",")
	}
	if v.Priority != nil {
		return "p" + strconv.FormatInt(*v.Priority, 10)
	}
	return ""
}

func tagList(tags []model.Tag) string {
	s := make([]string, len(tags))
	for i, t := range tags {
		s[i] = "#" + string(t)
	}
	return strings.Join(s, " ")
}
