package pick

import (
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// previewsFile holds each loaded task's preview, all but its notes, by line
// key: rendered from the load, so that blocks covers the whole tree
// (pick-spec.md, Preview), and rewritten by every load.
const previewsFile = "previews.json"

// previewEntry is one task's preview: the first line's left and right
// parts, aligned to the pane's width when shown; the lines after it; and
// the notes file.
type previewEntry struct {
	Left  string   `json:"left"`
	Right string   `json:"right"`
	Lines []string `json:"lines"`
	Notes string   `json:"notes"`
}

// loading is the preview while there is none to show: the session's file
// missing or unreadable, e.g. mid-write, or the key not in it yet.
const loading = "loading…"

// previews renders every task in the load. Text from data that may hold
// control characters (extra's keys and values) is escaped, as the preview
// shows it literally.
func previews(l *Load) map[string]previewEntry {
	blocks := map[model.ID][]model.ID{}
	for _, v := range l.Tasks {
		if v.Open() {
			for _, b := range v.BlockedBy {
				if !slices.Contains(blocks[b], v.ID) {
					blocks[b] = append(blocks[b], v.ID)
				}
			}
		}
	}
	out := make(map[string]previewEntry, len(l.Tasks))
	for _, v := range l.Tasks {
		bs := blocks[v.ID]
		slices.Sort(bs)
		out[key(v)] = preview(v, bs)
	}
	return out
}

func preview(v model.TaskView, blocks []model.ID) previewEntry {
	e := previewEntry{
		Left:  "#" + strconv.FormatInt(int64(v.ID), 10) + " " + string(v.Title),
		Right: string(v.Readiness),
		Notes: v.NotesPath,
	}
	if v.Priority != nil {
		e.Right += "  p" + strconv.FormatInt(*v.Priority, 10)
	}
	where := []string{string(v.Folder)}
	if len(v.Tags) > 0 {
		where = append(where, tagList(v.Tags))
	}
	where = append(where, "created "+date(v.CreatedAt), "updated "+date(v.UpdatedAt))
	if v.CompletedAt != nil {
		where = append(where, "completed "+date(*v.CompletedAt))
	}
	e.Lines = append(e.Lines,
		strings.Join(where, "   "),
		"blocked by: "+idList(v.BlockedBy)+"   blocks: "+idList(blocks),
	)
	if v.Extra != nil {
		for i, m := range v.Extra.Members {
			label := "extra: "
			if i > 0 {
				label = strings.Repeat(" ", len(label))
			}
			e.Lines = append(e.Lines, label+errs.OneLine(m.Key)+" = "+errs.OneLine(extraValue(m.Value)))
		}
	}
	return e
}

// extraValue is a string bare, anything else as compact JSON.
func extraValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := jsonio.MarshalLine(v)
	if err != nil {
		return "?"
	}
	return strings.TrimSuffix(string(b), "\n")
}

func date(t model.Timestamp) string {
	d, _, _ := strings.Cut(string(t), "T")
	return d
}

func idList(ids []model.ID) string {
	if len(ids) == 0 {
		return "—"
	}
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.FormatInt(int64(id), 10)
	}
	return strings.Join(s, ", ")
}

// writePreviews writes the load's previews into the session.
func writePreviews(s *Session, l *Load) *errs.Error {
	b, err := json.Marshal(previews(l))
	if err != nil {
		return errs.Internal("encoding previews: " + err.Error())
	}
	return s.Write(previewsFile, b)
}

// previewVerb prints the preview of the line whose key is args[0], for
// fzf's preview pane, as wide as FZF_PREVIEW_COLUMNS says. It never fails
// for want of the session's previews: it shows loading… instead.
func previewVerb(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	switch {
	case len(args) == 0:
		return nil, errs.Usage([]errs.UsageProblem{{Reason: "missing line key"}})
	case len(args) > 1:
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[1], Reason: "unexpected argument"}})
	}
	// A choose list's line is keyed as the task or folder it names.
	key := strings.TrimPrefix(args[0], choiceMark)
	// A folder's line, in a choose list of folders: its path.
	if strings.HasPrefix(key, "/") {
		return []byte(key + "\n"), nil
	}
	// A blocker with no task, in u's choose list: its ID alone.
	if !strings.Contains(key, "@") {
		return []byte("no task with ID " + key + "\n"), nil
	}
	b, ok, e := s.Read(previewsFile)
	var all map[string]previewEntry
	if e != nil || !ok || json.Unmarshal(b, &all) != nil {
		return []byte(loading + "\n"), nil
	}
	entry, ok := all[key]
	if !ok {
		return []byte(loading + "\n"), nil
	}
	environ := env.Sys.Environ()
	width := 80
	if n, err := strconv.Atoi(lookupEnv(environ, "FZF_PREVIEW_COLUMNS")); err == nil && n > 0 {
		width = n
	}
	var out strings.Builder
	gap := max(2, width-runewidth.StringWidth(entry.Left)-runewidth.StringWidth(entry.Right))
	out.WriteString(entry.Left + strings.Repeat(" ", gap) + entry.Right + "\n")
	for _, l := range entry.Lines {
		out.WriteString(l + "\n")
	}
	const rule = "── notes "
	out.WriteString(rule + strings.Repeat("─", max(0, width-runewidth.StringWidth(rule))) + "\n")
	out.WriteString(notes(env, entry.Notes, width, !noColor(environ)))
	return []byte(out.String()), nil
}

// notes renders a notes file: with glow if it is on PATH, else with bat, as
// Markdown, else as it is. A renderer that fails falls back to the next.
// An empty or missing file is (no notes).
func notes(env Env, path string, width int, color bool) string {
	b, err := env.Ops.FS.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) || err == nil && len(b) == 0:
		return "(no notes)\n"
	case err != nil:
		return "(notes unreadable: " + errs.OneLine(err.Error()) + ")\n"
	}
	environ := env.Sys.Environ()
	w := strconv.Itoa(width)
	if glow, err := env.Sys.LookPath("glow"); err == nil {
		style := "notty"
		if color {
			style = lookupEnv(environ, "GLAMOUR_STYLE")
			if style == "" {
				style = "dark"
			}
		}
		// glow writes plain text to a pipe unless color is forced.
		if out, _, status, err := env.Sys.Output(glow, []string{"-s", style, "-w", w, path}, append(slices.Clone(environ), "CLICOLOR_FORCE=1")); err == nil && status == 0 {
			return string(out)
		}
	}
	if bat, err := env.Sys.LookPath("bat"); err == nil {
		c := "never"
		if color {
			c = "always" // bat honors NO_COLOR only with --color=auto, which a pipe turns off anyway
		}
		if out, _, status, err := env.Sys.Output(bat, []string{"--color=" + c, "--style=plain", "--language=markdown", "--paging=never", "--terminal-width", w, path}, environ); err == nil && status == 0 {
			return string(out)
		}
	}
	s := string(b)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}
