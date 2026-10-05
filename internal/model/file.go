package model

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
)

// FileStatus is the outcome of checking a versioned file (see design-spec.md,
// File validity). Reading failures (not-json) happen before, in jsonio.
type FileStatus int

const (
	FileOK FileStatus = iota
	// FileCorrupt: the file fails a file-level rule; corrupt, reason invalid.
	FileCorrupt
	// FileUnsupported: the file's schema is not the supported version.
	FileUnsupported
)

// FileResult is a checked file's status. Versioned reports whether the file
// passed step 1 (parseable and versioned); Found is then its schema version,
// whatever the final Status — info reports it even for a file corrupt at
// step 3. Problems says what made it corrupt, each at a JSON Pointer into
// the file; the corrupt error reports them. SchemaProblems are those
// of a file corrupt at step 3 that its published schema also finds: not
// repeated keys, nor the rules beyond the schema (see Problems.SchemaList).
// The integer-literal rule is the exception: its problems are included,
// though the schema accepts an integral 42.0, because Int reports a
// fraction (which the schema rejects) and 42.0 alike. The agreement corpus
// leaves such numbers out (implementation-spec.md, Agreement tests).
type FileResult struct {
	Status         FileStatus
	Versioned      bool
	Found          int64
	Problems       []errs.Problem
	SchemaProblems []errs.Problem
}

func corrupt(problems ...errs.Problem) FileResult {
	return FileResult{Status: FileCorrupt, Problems: problems}
}

// checkVersion runs File validity steps 1 and 2 on a parsed file: schema
// present, unrepeated, and an integer literal within ±(2^53 − 1); then the
// supported version.
func checkVersion(obj *jsonio.Object, repeated []string, supported int64) (FileResult, bool) {
	if slices.Contains(repeated, "/schema") {
		return corrupt(errs.Problem{Field: "/schema", Reason: "repeated, so its version is ambiguous"}), false
	}
	v, ok := obj.Get("schema")
	if !ok {
		return corrupt(errs.Problem{Field: "/schema", Reason: reasonRequired}), false
	}
	var p Problems
	found, ok := p.Int(v, "/schema", -schemaMax, schemaMax)
	if !ok {
		return corrupt(p.List()...), false
	}
	if found != supported {
		return FileResult{Status: FileUnsupported, Versioned: true, Found: found}, false
	}
	return FileResult{Versioned: true, Found: found}, true
}

// schemaMax bounds a file's schema value: the integers every JSON reader
// holds exactly (see design-spec.md, File validity).
const schemaMax = 1<<53 - 1

// finish turns what the adapter found into the result of a file that passed
// steps 1 and 2 (version: that result): every repeated key and problem makes
// it corrupt.
func finish(version FileResult, repeated []string, p *Problems) FileResult {
	var out []errs.Problem
	for _, r := range repeated {
		out = append(out, errs.Problem{Field: r, Reason: "repeated key"})
	}
	out = append(out, p.List()...)
	if len(out) > 0 {
		r := corrupt(out...)
		r.Versioned, r.Found, r.SchemaProblems = true, version.Found, p.SchemaList()
		return r
	}
	return version
}

// DecodeTaskFile checks a parsed task file (repeated: its repeated keys, from
// jsonio) named by filenameID, and returns its content. The content is valid
// only when the result's Status is FileOK.
func DecodeTaskFile(obj *jsonio.Object, repeated []string, filenameID ID) (TaskFile, FileResult) {
	version, ok := checkVersion(obj, repeated, TaskSchema)
	if !ok {
		return TaskFile{}, version
	}
	var p Problems
	f, _ := p.Object(obj, "")
	t := TaskFile{Schema: TaskSchema}
	f.Required("schema") // already checked

	idOK := false // id passed its field rules (step 3), whether or not it matches the filename
	if v, ok := f.Required("id"); ok {
		if id, ok := p.ID(v, "/id"); ok {
			t.ID, idOK = id, true
			if id != filenameID {
				p.AddAdditional("/id", fmt.Sprintf("must match the ID in the filename (%d)", filenameID))
			}
		}
	}
	if v, ok := f.Required("title"); ok {
		if s, ok := p.String(v, "/title"); ok {
			t.Title, _ = p.StoredTitle(s, "/title")
		}
	}
	if v, ok := f.Required("priority"); ok {
		t.Priority, _ = p.Priority(v, "/priority")
	}
	if v, ok := f.Required("created_at"); ok {
		t.CreatedAt, _ = p.Timestamp(v, "/created_at")
	}
	if v, ok := f.Required("completed_at"); ok && v != nil {
		if ts, ok := p.timestampOrNull(v, "/completed_at"); ok {
			t.CompletedAt = &ts
		}
	}
	if v, ok := f.Required("updated_at"); ok {
		t.UpdatedAt, _ = p.Timestamp(v, "/updated_at")
	}
	if v, ok := f.Required("blocked_by"); ok {
		// The self check runs even when the set has another problem (a
		// duplicate, say), so each rule broken is named; an invalid item
		// is 0 in ids, which no task's ID is.
		ids, ok := p.IDs(v, "/blocked_by")
		if ok {
			t.BlockedBy = ids
		}
		if i := slices.Index(ids, t.ID); i >= 0 && idOK {
			p.AddAdditional(jsonio.Pointer("/blocked_by", strconv.Itoa(i)), "must not be the task's own ID")
		}
	}
	if v, ok := f.Required("tags"); ok {
		t.Tags, _ = p.Tags(v, "/tags")
	}
	if v, ok := f.Required("extra"); ok {
		if _, ok := p.Object(v, "/extra"); ok {
			t.Extra = v.(*jsonio.Object)
		}
	}
	f.Done()

	r := finish(version, repeated, &p)
	if r.Status != FileOK {
		return TaskFile{}, r
	}
	t.Normalize()
	return t, r
}

// timestampOrNull checks a non-null value where a timestamp or null is
// allowed.
func (p *Problems) timestampOrNull(v any, ptr string) (Timestamp, bool) {
	if _, ok := v.(string); !ok {
		p.Add(ptr, "expected a timestamp string or null")
		return "", false
	}
	return p.Timestamp(v, ptr)
}

// DecodeRootFile checks a parsed ftask.json (repeated: its repeated keys, from
// jsonio) and returns its content, valid only when Status is FileOK.
func DecodeRootFile(obj *jsonio.Object, repeated []string) (RootFile, FileResult) {
	version, ok := checkVersion(obj, repeated, RootSchema)
	if !ok {
		return RootFile{}, version
	}
	var p Problems
	f, _ := p.Object(obj, "")
	r := RootFile{Schema: RootSchema}
	f.Required("schema")
	if v, ok := f.Required("last_id"); ok {
		r.LastID, _ = p.Int(v, "/last_id", 0, IDMax)
	}
	f.Done()
	res := finish(version, repeated, &p)
	if res.Status != FileOK {
		return RootFile{}, res
	}
	return r, res
}
