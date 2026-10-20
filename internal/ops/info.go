package ops

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/phansen314/koan/internal/cli"
)

// InfoResult is info's result, in its schema's order.
type InfoResult struct {
	Root     string     `json:"root"`
	Tree     string     `json:"tree"`
	Archive  string     `json:"archive"`
	Database string     `json:"database"`
	State    string     `json:"state"`
	Format   *int       `json:"format"`
	NextID   *int64     `json:"next_id"`
	Tasks    *int64     `json:"tasks"`
	Folders  *int64     `json:"folders"`
	Claims   *int64     `json:"claims"`
	Pending  *int64     `json:"pending"`
	Stuck    *int64     `json:"stuck"`
	Error    *cli.Error `json:"error"`
}

// Info reports the root and its state, without the lock. It never fails
// for the root's state: it reports it.
func Info() *cli.Command {
	return &cli.Command{
		Name: "info",
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			root := env.Root()
			r := InfoResult{
				Root:     root,
				Tree:     filepath.Join(root, "tree"),
				Archive:  filepath.Join(root, "archived"),
				Database: filepath.Join(root, "koan.db"),
			}
			dir, e := resolveRoot(env)
			if e != nil {
				return nil, nil, e
			}
			if dir == "" {
				return notInitialized(r, root), nil, nil
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return nil, nil, cli.IO("could not list the root: "+err.Error(), root)
			}
			for _, de := range entries {
				if de.Name() == "koan.db" {
					return nil, nil, cli.NewError(cli.KindInternal, "info can't read a database yet")
				}
			}
			return notInitialized(r, r.Database), nil, nil
		},
	}
}

func notInitialized(r InfoResult, missing string) InfoResult {
	r.State = cli.KindNotInitialized
	r.Error = cli.NewError(cli.KindNotInitialized, missing+" does not exist. Run koan init.")
	r.Error.Paths = []string{missing}
	return r
}

// resolveRoot gives the directory the root is, following ~/koan-root when
// it is a symlink and no further link; "" when there is none.
func resolveRoot(env *cli.Env) (string, *cli.Error) {
	root := env.Root()
	fi, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", cli.IO("could not read the root: "+err.Error(), root)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return root, nil
	}
	target, err := os.Readlink(root)
	if err != nil {
		return "", cli.IO("could not read the root's symlink: "+err.Error(), root)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(env.Home, target)
	}
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", cli.IO("could not read the root: "+err.Error(), root)
	}
	return target, nil
}
