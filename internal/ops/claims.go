package ops

import (
	"crypto/rand"
	"encoding/hex"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// TokenSource makes a claim's token: 32 lowercase hexadecimal digits, new
// each time. Tests use a fixed sequence, so written files and results compare
// byte for byte.
type TokenSource func() string

// RealToken is 128 bits from the operating system's secure random source
// (design-spec.md, Tokens).
func RealToken() string {
	var b [16]byte
	rand.Read(b[:]) // never fails: crypto/rand aborts the process instead
	return hex.EncodeToString(b[:])
}

// optionalToken returns the token field, or "" when it is absent: the token
// of the caller's locked claim, for a targeting write.
func optionalToken(f *model.Fields, p *model.Problems) string {
	if v, ok := f.Optional("token"); ok {
		t, _ := p.Token(v, f.Ptr("token"))
		return t
	}
	return ""
}

// optionalExtra returns the extra field, or an empty map when it is absent.
func optionalExtra(f *model.Fields, p *model.Problems) *jsonio.Object {
	if v, ok := f.Optional("extra"); ok {
		if _, ok := p.Object(v, f.Ptr("extra")); ok {
			return v.(*jsonio.Object)
		}
	}
	return &jsonio.Object{}
}

// effectiveClaim is id's effective claim (design-spec.md, Effective claims):
// its entry in the claims file, when id has exactly one task file, usable and
// open. The caller has loaded the claims file, with Tx.Claims or
// Tx.ClaimsForRead. A task file is loaded only when id has an entry.
func effectiveClaim(tx *store.Tx, id model.ID) (model.ClaimEntry, bool) {
	entry, ok := tx.Claim(id)
	if !ok {
		return model.ClaimEntry{}, false
	}
	locs := tx.Copies(id)
	if len(locs) != 1 {
		return model.ClaimEntry{}, false
	}
	if ld := tx.Load(locs[0]); ld.State != store.Usable || !ld.Task.Open() {
		return model.ClaimEntry{}, false
	}
	return entry, true
}

// claimOf is id's effective claim as a task view shows it, nil when it has
// none.
func claimOf(tx *store.Tx, id model.ID) *model.Claim {
	if entry, ok := effectiveClaim(tx, id); ok {
		return entry.View()
	}
	return nil
}

// claimConflict is the conflict under rule for ids, each with its effective
// claim, or nil, as claims[i].
func claimConflict(tx *store.Tx, rule errs.Rule, ids []model.ID) *errs.Error {
	claims := make([]any, len(ids))
	for i, id := range ids {
		if c := claimOf(tx, id); c != nil {
			claims[i] = c
		}
	}
	return errs.ClaimConflict(rule, int64s(ids), claims)
}

// checkTokens is the targeting rule (operations.md, Targeting writes) for a
// write that targets ids and was given token, "" for none: a task with a
// locked effective claim needs that claim's token, and any other task refuses
// one. It returns the token-mismatch conflict for every task refused,
// ascending, or nil. The caller has loaded the claims file with Tx.Claims.
//
// Whether a claim is effective needs the task's own file. It is loaded only
// for a task that has an entry, and then only when the entry has a token or
// one was given: otherwise the write proceeds whatever the answer. A file
// that fails to load makes the claim inert, never the write fail.
func checkTokens(tx *store.Tx, ids []model.ID, token string) *errs.Error {
	var refused []model.ID
	for _, id := range ids {
		entry, has := tx.Claim(id)
		if !has {
			if token != "" {
				refused = append(refused, id)
			}
			continue
		}
		if entry.Token == "" && token == "" {
			continue
		}
		if _, effective := effectiveClaim(tx, id); !effective {
			if token != "" {
				refused = append(refused, id)
			}
			continue
		}
		if entry.Token == "" || token != entry.Token {
			refused = append(refused, id)
		}
	}
	if len(refused) == 0 {
		return nil
	}
	slices.Sort(refused)
	return claimConflict(tx, errs.RuleTokenMismatch, slices.Compact(refused))
}

// findOneClaimed is findOne for an operation that needs the claims file: it
// loads the file between the ID lookup and the load of the task file, so its
// error comes after not-found and before a needed task file's and every
// conflict (operations.md, Precedence).
func findOneClaimed(tx *store.Tx, id model.ID) (*store.Loaded, *errs.Error) {
	if e := tx.RequireWholeTree(tx.Index()); e != nil {
		return nil, e
	}
	if len(tx.Copies(id)) == 0 {
		return nil, errs.NotFound(nil, []int64{int64(id)}, nil)
	}
	if e := tx.Claims(); e != nil {
		return nil, e
	}
	return findOne(tx, id)
}

// dropClaims removes the claim entries of tasks that are now done or gone:
// the last step of done, delete, and delete-folder. The entries are inert
// already, so an OS error is the claims-not-updated warning, with its code
// when it has a symbolic name, and never fails the operation. Any other
// error is a bug, and is returned.
func dropClaims(tx *store.Tx, ids []model.ID) *errs.Error {
	left, e, err := tx.DropClaimsRaw(ids)
	switch {
	case e != nil:
		return e
	case err != nil:
		code, _ := tx.ClaimsCode(err)
		tx.Warn(errs.ClaimsNotUpdated(tx.ClaimsPath(), int64s(left), code))
	}
	return nil
}

// ClaimInput is claim's input.
type ClaimInput struct {
	ID       model.ID
	Extra    *jsonio.Object
	UseToken bool
}

func decodeClaim(f *model.Fields, p *model.Problems) any {
	return ClaimInput{ID: requiredID(f, p, "id"), Extra: optionalExtra(f, p), UseToken: optionalBool(f, p, "use_token", false)}
}

// ClaimOutput is claim's and claim-next's result (claim-output,
// claim-next-output): the task claimed, nil for a claim-next with nothing to
// claim, and the claim's token when it was made with one.
type ClaimOutput struct {
	Task  *model.TaskView `json:"task"`
	Token string          `json:"token,omitempty"`
}

// putClaim records a new claim on id, with extra and, on request, a new
// token, and returns the token, "" for none.
func putClaim(env Env, tx *store.Tx, id model.ID, extra *jsonio.Object, useToken bool) (string, *errs.Error) {
	entry := model.ClaimEntry{ClaimedAt: model.TimestampOf(env.Clock()), Extra: extra}
	if useToken {
		if env.Token == nil {
			return "", errs.Internal("no token source")
		}
		entry.Token = env.Token()
	}
	return entry.Token, tx.PutClaim(id, entry)
}

// runClaim claims the one task with id, under the write lock: refused when
// the task is done, or has an effective claim, whoever made it. A task that
// can be claimed — open, with one usable task file — has an effective claim
// exactly when the claims file holds an entry for it, so claim only ever
// adds an entry. No task file is written.
func runClaim(env Env, in ClaimInput, w *errs.Collector) (any, *errs.Error) {
	var out ClaimOutput
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		ld, e := findOneClaimed(tx, in.ID)
		if e != nil {
			return e
		}
		if !ld.Task.Open() {
			return errs.Conflict(errs.RuleTaskDone, []int64{int64(in.ID)})
		}
		if _, claimed := effectiveClaim(tx, in.ID); claimed {
			return claimConflict(tx, errs.RuleClaimed, []model.ID{in.ID})
		}
		// The view is built first, since building it can fail: once the
		// claim is recorded, nothing may.
		v, e := view(tx, ld)
		if e != nil {
			return e
		}
		token, e := putClaim(env, tx, in.ID, in.Extra, in.UseToken)
		if e != nil {
			return e
		}
		v.Claim = claimOf(tx, in.ID)
		out = ClaimOutput{Task: &v, Token: token}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// ClaimNextInput is claim-next's input, defaults applied.
type ClaimNextInput struct {
	Folder    model.FolderPath
	Recursive bool
	TagsAny   []model.Tag
	TagsAll   []model.Tag
	Extra     *jsonio.Object
	UseToken  bool
}

func decodeClaimNext(f *model.Fields, p *model.Problems) any {
	return ClaimNextInput{
		Folder:    optionalFolder(f, p, "folder"),
		Recursive: optionalBool(f, p, "recursive", true),
		TagsAny:   optionalTagSet(f, p, "tags_any"),
		TagsAll:   optionalTagSet(f, p, "tags_all"),
		Extra:     optionalExtra(f, p),
		UseToken:  optionalBool(f, p, "use_token", false),
	}
}

// runClaimNext claims the first task of the frontier that nobody has claimed,
// under the write lock: frontier's own query, then the first candidate whose
// ID has one task file in the whole tree and no effective claim. With none,
// it succeeds with no task and writes nothing. A candidate passed over for a
// second copy is a duplicate-id warning naming every copy.
func runClaimNext(env Env, in ClaimNextInput, w *errs.Collector) (any, *errs.Error) {
	var out ClaimOutput
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		q := scopeQuery{in: ScopeInput{Folder: in.Folder, Recursive: in.Recursive}, claims: (*store.Tx).Claims, wholeTree: true}
		ready, e := q.frontier(tx)
		if e != nil {
			return e
		}
		tags := Narrowing{TagsAny: in.TagsAny, TagsAll: in.TagsAll}
		for _, v := range ready {
			if !tags.keeps(v) {
				continue
			}
			if copies := tx.Copies(v.ID); len(copies) != 1 {
				duplicateID(tx, v.ID, copies)
				continue
			}
			if v.Claim != nil {
				continue
			}
			token, e := putClaim(env, tx, v.ID, in.Extra, in.UseToken)
			if e != nil {
				return e
			}
			v.Claim = claimOf(tx, v.ID)
			out = ClaimOutput{Task: &v, Token: token}
			return nil
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// TokenInput is the input of release, done, and delete: one task, and the
// token of the caller's locked claim on it, "" for none.
type TokenInput struct {
	ID    model.ID
	Token string
}

func decodeTokenInput(f *model.Fields, p *model.Problems) any {
	return TokenInput{ID: requiredID(f, p, "id"), Token: optionalToken(f, p)}
}

func decodeRelease(f *model.Fields, p *model.Problems) any      { return decodeTokenInput(f, p) }
func decodeForceRelease(f *model.Fields, p *model.Problems) any { return decodeID(f, p) }

// runRelease clears the task's claim, whoever made it, when the targeting
// rule lets it through: a locked claim needs its token.
func runRelease(env Env, in TokenInput, w *errs.Collector) (any, *errs.Error) {
	return release(env, in.ID, in.Token, true, w)
}

// runForceRelease clears the task's claim, locked or not, checking no token:
// the one way past a lock.
func runForceRelease(env Env, in IDInput, w *errs.Collector) (any, *errs.Error) {
	return release(env, in.ID, "", false, w)
}

// release removes the entry of the one task with id from the claims file,
// under the write lock, whether its claim was effective or inert; changed
// says whether an effective one was cleared. With no entry nothing is
// written. No task file is written either.
func release(env Env, id model.ID, token string, check bool, w *errs.Collector) (any, *errs.Error) {
	var out ChangedOutput
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		ld, e := findOneClaimed(tx, id)
		if e != nil {
			return e
		}
		if check {
			if e := checkTokens(tx, []model.ID{id}, token); e != nil {
				return e
			}
		}
		_, out.Changed = effectiveClaim(tx, id)
		if _, e := tx.DropClaims([]model.ID{id}); e != nil {
			return e
		}
		out.Task = tx.Task(ld)
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
