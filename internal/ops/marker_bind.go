package ops

import (
	"os"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/markers"
	"github.com/phansen314/koan/internal/store"
)

// MarkerBindResult is marker bind's result, in its schema's order.
type MarkerBindResult struct {
	Marker   markers.Marker `json:"marker"`
	File     string         `json:"file"`
	Replaced bool           `json:"replaced"`
}

// MarkerBind binds a directory outside the tree to a folder. It writes
// no row and nothing in the tree, so it takes no lock: it reads the
// database as a read does, then writes the marker whole.
func MarkerBind() *cli.Command {
	return &cli.Command{
		Name:    "marker bind",
		Subject: &cli.Option{Field: "folder", Required: true, Complete: foldersOnly.complete()},
		Options: []cli.Option{{Field: "dir", Kind: cli.Path}, {Field: "force", Kind: cli.Bool}},
		Here:    &cli.Here{Field: "dir"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("folder", core.WantFolder, "")
			if ref.IsRoot() {
				r.fault("/folder", "the root can't be bound: it has no ID")
			}
			dir := r.absPath("dir", true)
			force := r.boolean("force", false)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			real, e := markerDir(s.Root, dir)
			if e != nil {
				return nil, nil, e
			}
			res := MarkerBindResult{File: markers.File(dir)}
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				id, err := resolveFolder(tx, ref, "folder")
				if err != nil {
					return nil, err
				}
				names, _, err := koanFiles(dir)
				if err != nil {
					return nil, err
				}
				if !force {
					checkout, err := markers.Checkout(dir)
					if err != nil {
						return nil, lookIO(err)
					}
					if checkout {
						return nil, core.Conflict(core.RuleCheckout, nil, []string{dir},
							"%s is a checkout: its .git is a directory, and a marker there would bind every directory under it. Bind a worktree, or use --force", dir)
					}
				}
				res.Replaced = slices.Contains(names, markers.FileName)
				if res.Replaced && !force {
					m, why := markers.Read(res.File, real)
					if n, ok := tx.Node(m.ID); why == "" && ok && n.Kind == core.NodeFolder && m.ID != id {
						return nil, core.Conflict(core.RuleBound, []int64{m.ID}, []string{res.File},
							"%s is bound to @%d (%s); use --force to bind it to @%d instead", dir, m.ID, tx.Path(m.ID), id)
					}
				}
				res.Marker = markers.Marker{Schema: markers.Schema, ID: id, Path: tx.Path(id), Dir: real}
				return nil, nil
			})
			if e != nil {
				return nil, nil, e
			}
			if notDir, err := markers.NotDir(dir); err != nil || notDir {
				at := dir + "/" + markers.DirName
				return nil, nil, cli.IO("could not write the marker: "+at+" is not a directory", at)
			}
			if err := markers.Write(dir, res.Marker); err != nil {
				return nil, nil, writeIO(err, res.File)
			}
			return res, warnings, nil
		},
	}
}

// markerDir checks a directory a marker is written in, after the root's
// state: it is not under ~/koan-root, judged by real paths, since
// markers there are never read, and it is a directory that exists. It
// gives the directory's real path.
func markerDir(root store.Root, dir string) (string, *cli.Error) {
	real, exists, e := notUnder(root, dir, "where markers are never read")
	if e != nil {
		return "", e
	}
	if !exists {
		return "", cli.IO(dir+" does not exist", dir)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", cli.IO(dir+" is not a directory", dir)
	}
	return real, nil
}

// notUnder checks that a directory, given at /dir, is not under
// ~/koan-root, judged by real paths, so no second route gets past it: the
// root's real path is worked out first, then the directory's below it.
// why ends the refusal's reason. It gives the directory's real path, and
// whether all of it exists.
func notUnder(root store.Root, dir, why string) (real string, exists bool, e *cli.Error) {
	rootReal, _, err := markers.Real(root.Path, "")
	if err == nil {
		if real, exists, err = markers.Real(dir, rootReal); err == nil && markers.Under(real, rootReal) {
			reason := dir + " is under " + root.Path + ", " + why
			return "", false, cli.InvalidInput(reason, cli.Problem{Field: "/dir", Reason: reason})
		}
	}
	if err != nil {
		return "", false, cli.IO("could not look for "+dir+": "+err.Error(), dir)
	}
	return real, exists, nil
}

// koanFiles is what a directory's .koan/ holds, ok false when there is
// none; one holding anything that isn't koan's own is foreign-files, with
// those files.
func koanFiles(dir string) (names []string, ok bool, err error) {
	names, ok, err = markers.Contents(dir)
	if err != nil {
		return nil, false, lookIO(err)
	}
	var foreign []string
	for _, n := range markers.Foreign(names) {
		foreign = append(foreign, dir+"/"+markers.DirName+"/"+n)
	}
	if len(foreign) > 0 {
		return nil, false, core.Conflict(core.RuleForeignFiles, nil, foreign,
			"%s/%s holds files that aren't koan's: %s. Move them out first", dir, markers.DirName, strings.Join(foreign, ", "))
	}
	return names, ok, nil
}

// writeIO is the operating system refusing to write or remove a marker.
func writeIO(err error, file string) *cli.Error {
	return cli.IO("could not write the marker "+file+": "+err.Error(), file)
}
