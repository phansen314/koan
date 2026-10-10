package migrations

import (
	"encoding/json"
	"errors"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
)

// Step 1, tree-marker: koan.json 1 → 2 removes `last_id`, which the state file
// holds now, and adds `migration`, as 0. Task files are unchanged. (The
// last_id it removes is migrate's to carry over: a step sees one file only.)

// rootSchema1 is the JSON Schema of koan.json at schema 1: exactly `schema`
// and `last_id`, and no other key.
const rootSchema1 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "root-file-1",
  "type": "object",
  "required": ["schema", "last_id"],
  "properties": {
    "schema": { "const": 1 },
    "last_id": { "type": "integer", "minimum": 0, "maximum": 999999999999999 }
  },
  "additionalProperties": false
}`

// checkRoot1 is rootSchema1's rules, with the file-level ones every format
// shares: no repeated key, integers written as integer literals.
func checkRoot1(obj *jsonio.Object, repeated []string) []errs.Problem {
	var p model.Problems
	f, _ := p.Object(obj, "")
	f.Required("schema") // already checked
	if v, ok := f.Required("last_id"); ok {
		p.Int(v, "/last_id", 0, model.IDMax)
	}
	f.Done()
	return append(repeatedProblems(repeated), p.List()...)
}

// convertRoot1 sets schema 2, drops `last_id`, and appends `migration`, 0: a
// schema 1 file predates the counter. Which step the tree records is
// `migrate`'s to set, once every file is converted.
func convertRoot1(obj *jsonio.Object) (*jsonio.Object, error) {
	if _, ok := obj.Get("last_id"); !ok {
		return nil, errors.New("koan.json has no last_id")
	}
	out := &jsonio.Object{}
	for _, m := range obj.Members {
		switch m.Key {
		case "last_id":
			continue
		case "schema":
			m.Value = json.Number("2")
		}
		out.Members = append(out.Members, m)
	}
	out.Members = append(out.Members, jsonio.Member{Key: "migration", Value: json.Number("0")})
	return out, nil
}
