package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
)

// Rotated is the data of rotated, and what log rotate moved: the file it
// wrote, the first and last seq that file holds, and how many events.
type Rotated struct {
	File     string `json:"file"`
	FirstSeq int64  `json:"first_seq"`
	LastSeq  int64  `json:"last_seq"`
	Count    int    `json:"count"`
}

// LogFileSchema is the format a rotated file's lines name.
const LogFileSchema = 1

// logLine is one line of a rotated file: the event as log list prints it,
// its schema first.
type logLine struct {
	Schema int `json:"schema"`
	Event
}

// rotatedName is a rotated file's name, by its first and last seq; a
// leftover temp file adds .tmp to it.
var (
	rotatedName = regexp.MustCompile(`^([0-9]{16})-([0-9]{16})\.jsonl$`)
	rotatedTemp = regexp.MustCompile(`^[0-9]{16}-[0-9]{16}\.jsonl\.tmp$`)
)

// Rotate moves the oldest events, in seq order, up to the first at or
// after before, into a file in log/, then deletes their rows and records
// rotated, in one transaction. It holds the write lock throughout, and
// first carries out the pending steps, as every write does. A file of
// koan's name whose whole range is still in the table, left by a run cut
// short, is removed before anything is written, and so is a leftover
// temp file; nothing else in log/ is judged. With nothing to rotate it
// gives nil and records nothing.
func (s *Store) Rotate(before string) (moved *Rotated, warnings []cli.Warning, e *cli.Error) {
	e = s.Hold(func(h *Held) *cli.Error {
		defer func() { warnings = h.Left() }()
		if e := h.Finish(); e != nil {
			return e
		}
		var e *cli.Error
		moved, e = h.rotate(before)
		return e
	})
	return moved, warnings, e
}

func (h *Held) rotate(before string) (*Rotated, *cli.Error) {
	root := h.s.Root
	dir := root.Log
	if err := os.Mkdir(dir, 0o755); err == nil {
		if e := syncDir(root.Dir, root.Path); e != nil {
			return nil, e
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return nil, cli.IO("could not make "+dir+": "+err.Error(), dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, cli.IO("could not list "+dir+": "+err.Error(), dir)
	}

	var stale []string
	var moving EventFilter
	moved := &Rotated{}
	e := h.s.Read(func(tx *Tx) error {
		for _, entry := range entries {
			name := entry.Name()
			if !entry.Type().IsRegular() {
				continue
			}
			if rotatedTemp.MatchString(name) {
				stale = append(stale, name)
				continue
			}
			m := rotatedName.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			first, err1 := strconv.ParseInt(m[1], 10, 64)
			last, err2 := strconv.ParseInt(m[2], 10, 64)
			if err1 != nil || err2 != nil || first > last {
				continue
			}
			var n int64
			if err := tx.QueryRow("SELECT count(*) FROM event WHERE seq BETWEEN ? AND ?", first, last).Scan(&n); err != nil {
				return err
			}
			if n == last-first+1 {
				stale = append(stale, name)
			}
		}
		var cut int64
		if err := tx.QueryRow("SELECT coalesce(min(seq), 0) FROM event WHERE at >= ?", before).Scan(&cut); err != nil {
			return err
		}
		// With no event at or after it, every event moves.
		moving = EventFilter{Below: cut}
		where, args := moving.where()
		return tx.QueryRow("SELECT coalesce(min(seq), 0), coalesce(max(seq), 0), count(*) FROM event"+where, args...).
			Scan(&moved.FirstSeq, &moved.LastSeq, &moved.Count)
	})
	if e != nil {
		return nil, e
	}
	for _, name := range stale {
		p := filepath.Join(dir, name)
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, cli.IO("could not remove "+p+", left by a run cut short: "+err.Error(), p)
		}
	}
	if len(stale) > 0 {
		if e := syncDir(dir, dir); e != nil {
			return nil, e
		}
	}
	if moved.Count == 0 {
		return nil, nil
	}

	name := fmt.Sprintf("%016d-%016d.jsonl", moved.FirstSeq, moved.LastSeq)
	moved.File = filepath.Join(dir, name)
	e = h.s.Read(func(tx *Tx) error {
		return writeLog(moved.File, func(fn func(Event) error) error { return tx.EachEvent(moving, fn) })
	})
	if e != nil {
		return nil, e
	}
	if err := fault("log written"); err != nil {
		return nil, cli.IO("could not write "+moved.File+": "+err.Error(), moved.File)
	}
	if err := os.Rename(moved.File+".tmp", moved.File); err != nil {
		return nil, cli.IO("could not rename "+moved.File+".tmp into place: "+err.Error(), moved.File)
	}
	if e := syncDir(dir, dir); e != nil {
		return nil, e
	}
	if err := fault("log renamed"); err != nil {
		return nil, cli.IO("could not flush "+dir+": "+err.Error(), dir)
	}

	_, _, e = h.Change(func(tx *Tx) (any, []cli.Warning, error) {
		if _, err := tx.Exec("DELETE FROM event WHERE seq BETWEEN ? AND ?", moved.FirstSeq, moved.LastSeq); err != nil {
			return nil, nil, err
		}
		tx.Record(EventRotated, 0, Place{Folder: core.RootID, Path: "/"}, *moved)
		return nil, nil, nil
	})
	if e != nil {
		return nil, e
	}
	return moved, nil
}

// writeLog writes the events each gives to file's temp file as it gives
// them, one line each, as log list prints them with their schema first,
// without escaping <, > and &, and flushes it. An error is an Err, or
// each's own.
func writeLog(file string, each func(fn func(Event) error) error) error {
	tmp := file + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return Err(cli.IO("could not write "+tmp+": "+err.Error(), tmp))
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false)
	writeErr := func(err error) error { return Err(cli.IO("could not write "+tmp+": "+err.Error(), tmp)) }
	err = each(func(ev Event) error {
		line.Reset()
		if err := enc.Encode(logLine{Schema: LogFileSchema, Event: ev}); err != nil {
			return Err(cli.NewError(cli.KindInternal, "could not encode event "+strconv.FormatInt(ev.Seq, 10)+": "+err.Error()))
		}
		if _, err := w.Write(line.Bytes()); err != nil {
			return writeErr(err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return writeErr(err)
	}
	if err := f.Sync(); err != nil {
		return writeErr(err)
	}
	if err := f.Close(); err != nil {
		return writeErr(err)
	}
	return nil
}
