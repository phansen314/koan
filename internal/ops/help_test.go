package ops

import (
	"strings"
	"testing"
)

// Every command, option and convenience has its line in help, and every
// entry of the tables is used.
func TestDescribed(t *testing.T) {
	used := map[string]bool{}
	for _, c := range Commands() {
		if c.Short == "" {
			t.Errorf("%s: no line in help", c.Name)
		}
		used[c.Name] = true
		for _, o := range c.Options {
			if o.Usage == "" {
				t.Errorf("%s --%s: no line in help", c.Name, o.Field)
			}
			if _, ok := usages[c.Name+"/"+o.Field]; ok {
				used[c.Name+"/"+o.Field] = true
			} else {
				used[o.Field] = true
			}
		}
		for _, f := range c.Files {
			if f.Usage == "" {
				t.Errorf("%s %s: no line in help", c.Name, f.Name)
			}
		}
		if h := c.Here; h != nil && h.Flag != "" && h.Usage == "" {
			t.Errorf("%s %s: no line in help", c.Name, h.Flag)
		}
	}
	for name := range hereUsages {
		if !used[name] {
			t.Errorf("hereUsages: %s is no command", name)
		}
	}
	for name := range shorts {
		if !used[name] {
			t.Errorf("shorts: %s is no command", name)
		}
	}
	for key := range usages {
		if !used[key] {
			t.Errorf("usages: %s is never used", key)
		}
	}
}

// --here has one wording everywhere, a folder's material is "material",
// unclaim ends claims whoever holds them, batch's usage shows -i, and a
// folder, --file and --here that exclude each other show so.
func TestHelpWording(t *testing.T) {
	for _, c := range Commands() {
		if h := c.Here; h != nil && h.Flag != "" && h.Usage != "the folder the working directory is in" {
			t.Errorf("%s %s: %q", c.Name, h.Flag, h.Usage)
		}
		for _, o := range c.Options {
			if strings.Contains(o.Usage, "of yours") {
				t.Errorf("%s --%s: %q", c.Name, o.Field, o.Usage)
			}
		}
		exclusive := c.Here != nil && c.Here.Flag != "" && c.Name != "where"
		shown := len(c.Uses) > 0 && c.Uses[0] == exclusiveFolder
		if exclusive != shown {
			t.Errorf("%s: usage lines %q", c.Name, c.Uses)
		}
		switch c.Name {
		case "unclaim":
			if !strings.Contains(c.Short, "whoever holds them") {
				t.Errorf("unclaim: %q", c.Short)
			}
		case "batch":
			if len(c.Uses) != 1 || !strings.HasPrefix(c.Uses[0], "-i ") {
				t.Errorf("batch: %q", c.Uses)
			}
		}
	}
}
