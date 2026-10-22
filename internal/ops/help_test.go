package ops

import "testing"

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
