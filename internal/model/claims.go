package model

import (
	"regexp"
	"slices"
	"strconv"

	"github.com/phansen314/koan/internal/jsonio"
)

// ClaimEntry is one entry of the claims file (see design-spec.md, Claims
// file): a claim on one task. Token is "" for a claim made without one.
type ClaimEntry struct {
	ClaimedAt Timestamp
	Extra     *jsonio.Object
	Token     string
}

// Claim is a claim as a task view and an error's claims show it (task-view's
// claim). It has no token: nothing built from it can show one.
type Claim struct {
	ClaimedAt Timestamp      `json:"claimed_at"`
	Extra     *jsonio.Object `json:"extra"`
	Locked    bool           `json:"locked"`
}

// View is e as it is shown: without its token, which Locked stands for.
func (e ClaimEntry) View() *Claim {
	extra := e.Extra
	if extra == nil {
		extra = &jsonio.Object{}
	}
	return &Claim{ClaimedAt: e.ClaimedAt, Extra: extra, Locked: e.Token != ""}
}

// ClaimsFile is the claims file's content: this machine's claims on one
// root's tasks, by task ID.
type ClaimsFile struct {
	Schema int64
	Root   string
	Claims map[ID]ClaimEntry
}

// Encode returns the claims file's bytes, per design-spec.md, File format:
// the entries in ascending order of ID, each extra's keys in their own order.
func (c ClaimsFile) Encode() ([]byte, error) {
	ids := make([]ID, 0, len(c.Claims))
	for id := range c.Claims {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	claims := &jsonio.Object{}
	for _, id := range ids {
		e := c.Claims[id]
		extra := e.Extra
		if extra == nil {
			extra = &jsonio.Object{}
		}
		entry := &jsonio.Object{Members: []jsonio.Member{
			{Key: "claimed_at", Value: e.ClaimedAt},
			{Key: "extra", Value: extra},
		}}
		if e.Token != "" {
			entry.Members = append(entry.Members, jsonio.Member{Key: "token", Value: e.Token})
		}
		claims.Members = append(claims.Members, jsonio.Member{Key: strconv.FormatInt(int64(id), 10), Value: entry})
	}
	return jsonio.MarshalFile(struct {
		Schema int64          `json:"schema"`
		Root   string         `json:"root"`
		Claims *jsonio.Object `json:"claims"`
	}{c.Schema, c.Root, claims})
}

// tokenPattern is a token's form: 32 lowercase hexadecimal digits.
var tokenPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Token checks v, at ptr, as a claim's token.
func (p *Problems) Token(v any, ptr string) (string, bool) {
	s, ok := p.String(v, ptr)
	if !ok {
		return "", false
	}
	if !tokenPattern.MatchString(s) {
		p.Add(ptr, "must be 32 lowercase hexadecimal digits")
		return "", false
	}
	return s, true
}

// idKeyPattern is a task ID as the claims file's keys write it.
var idKeyPattern = regexp.MustCompile(`^[1-9][0-9]{0,14}$`)

// DecodeClaimsFile checks a parsed claims file (repeated: its repeated keys,
// from jsonio) and returns its content, valid only when Status is FileOK.
// Whether it names the right root is the caller's to compare.
func DecodeClaimsFile(obj *jsonio.Object, repeated []string) (ClaimsFile, FileResult) {
	version, ok := checkVersion(obj, repeated, ClaimsSchema)
	if !ok {
		return ClaimsFile{}, version
	}
	var p Problems
	f, _ := p.Object(obj, "")
	c := ClaimsFile{Schema: ClaimsSchema, Claims: map[ID]ClaimEntry{}}
	f.Required("schema")
	if v, ok := f.Required("root"); ok {
		if str, ok := p.String(v, "/root"); ok {
			if str == "" {
				p.Add("/root", "must not be empty")
			}
			c.Root = str
		}
	}
	if v, ok := f.Required("claims"); ok {
		if _, ok := p.Object(v, "/claims"); ok {
			for _, m := range v.(*jsonio.Object).Members {
				at := jsonio.Pointer("/claims", m.Key)
				if !idKeyPattern.MatchString(m.Key) {
					p.Add(at, "must be keyed by a task ID")
					continue
				}
				id, _ := strconv.ParseInt(m.Key, 10, 64)
				if e, ok := decodeClaimEntry(&p, m.Value, at); ok {
					c.Claims[ID(id)] = e
				}
			}
		}
	}
	f.Done()
	res := finish(version, repeated, &p)
	if res.Status != FileOK {
		return ClaimsFile{}, res
	}
	return c, res
}

func decodeClaimEntry(p *Problems, v any, at string) (ClaimEntry, bool) {
	f, ok := p.Object(v, at)
	if !ok {
		return ClaimEntry{}, false
	}
	var e ClaimEntry
	if v, has := f.Required("claimed_at"); has {
		var valid bool
		e.ClaimedAt, valid = p.Timestamp(v, f.Ptr("claimed_at"))
		ok = ok && valid
	} else {
		ok = false
	}
	if v, has := f.Required("extra"); has {
		if _, valid := p.Object(v, f.Ptr("extra")); valid {
			e.Extra = v.(*jsonio.Object)
		} else {
			ok = false
		}
	} else {
		ok = false
	}
	if v, has := f.Optional("token"); has {
		var valid bool
		e.Token, valid = p.Token(v, f.Ptr("token"))
		ok = ok && valid
	}
	f.Done()
	return e, ok
}
