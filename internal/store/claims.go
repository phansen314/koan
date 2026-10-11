package store

import (
	"path"
	"slices"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
)

// ClaimsName is the claims file's name in the config directory.
const ClaimsName = "claims.json"

// ClaimsPath is the claims file's path; "" when the config directory is
// unknown.
func (e Env) ClaimsPath() string {
	if e.ConfigDir == "" {
		return ""
	}
	return path.Join(e.ConfigDir, ClaimsName)
}

// ClaimsState is the claims file's outcome, as info reports it
// (implementation-spec.md, Claims file). Missing and other-root both mean a
// root with no claims.
type ClaimsState string

const (
	ClaimsMissing     ClaimsState = "missing"
	ClaimsOtherRoot   ClaimsState = "other-root"
	ClaimsUnreadable  ClaimsState = "unreadable"
	ClaimsCorrupt     ClaimsState = "corrupt"
	ClaimsUnsupported ClaimsState = "unsupported-format"
	ClaimsOK          ClaimsState = "ok"
)

// claimsResult is what reading the claims file for a root found.
type claimsResult struct {
	state ClaimsState
	err   error             // ClaimsUnreadable: the OS error
	cause errs.CorruptCause // ClaimsCorrupt
	found int64             // ClaimsUnsupported: its schema
	dir   bool              // ClaimsCorrupt: a directory is in its place
	file  model.ClaimsFile  // ClaimsOK
}

// readClaims reads the claims file by path, outside the root, as a regular
// file: anything else in its place is corrupt (unexpected-file). root is the
// config's root, cleaned and with ~/ expanded: a valid file naming any other
// is other-root, which for this root holds no claims.
func readClaims(env Env, root string) claimsResult {
	p := env.ClaimsPath()
	if p == "" {
		return claimsResult{state: ClaimsMissing}
	}
	fi, err := env.FS.Lstat(p)
	if err != nil {
		return claimsReadError(err)
	}
	if !fi.Mode().IsRegular() {
		return claimsResult{state: ClaimsCorrupt, cause: unexpectedFile, dir: fi.IsDir()}
	}
	data, err := env.FS.ReadFile(p)
	if err != nil {
		return claimsReadError(err)
	}
	obj, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		return claimsResult{state: ClaimsCorrupt, cause: notJSON(err)}
	}
	file, res := model.DecodeClaimsFile(obj, repeated)
	switch res.Status {
	case model.FileUnsupported:
		return claimsResult{state: ClaimsUnsupported, found: res.Found}
	case model.FileCorrupt:
		return claimsResult{state: ClaimsCorrupt, cause: invalid(res)}
	}
	if file.Root != root {
		return claimsResult{state: ClaimsOtherRoot}
	}
	return claimsResult{state: ClaimsOK, file: file}
}

func claimsReadError(err error) claimsResult {
	if isErrno(err, syscall.ENOENT) {
		return claimsResult{state: ClaimsMissing}
	}
	return claimsResult{state: ClaimsUnreadable, err: err}
}

// claimsError is the error of a claims file that is unusable, for an
// operation that needs it: io, corrupt, or unsupported-format. It is nil for
// one that is ok, missing, or another root's.
func claimsError(env Env, cr claimsResult) *errs.Error {
	p := env.ClaimsPath()
	switch cr.state {
	case ClaimsUnreadable:
		return errs.FromOS(p, cr.err)
	case ClaimsUnsupported:
		return errs.UnsupportedFormat(p, cr.found, []int64{model.ClaimsSchema})
	case ClaimsCorrupt:
		return errs.CorruptBy(p, cr.cause)
	}
	return nil
}

// loadClaims reads the claims file on first use and keeps the outcome for the
// transaction step.
func (tx *Tx) loadClaims() claimsResult {
	if !tx.claimsRead {
		tx.claims, tx.claimsRead = readClaims(tx.env, tx.rootPath), true
	}
	return tx.claims
}

// Claims loads the claims file for an operation that needs it, and returns
// the error an unusable one is: io, corrupt, or unsupported-format. A missing
// file, or another root's, holds no claims. In a write the first call is
// under the lock, so the file is current. Claim then reads the entries.
func (tx *Tx) Claims() *errs.Error {
	return claimsError(tx.env, tx.loadClaims())
}

// ClaimsForRead loads the claims file for a read. An unusable one is no
// error: it holds no entries, and is recorded as one unusable-file warning.
// The error is internal only: an OS error with no symbolic name.
func (tx *Tx) ClaimsForRead() *errs.Error {
	cr := tx.loadClaims()
	p := tx.env.ClaimsPath()
	switch cr.state {
	case ClaimsUnreadable:
		e := errs.FromOS(p, cr.err)
		d, ok := e.Details.(errs.IODetails)
		if !ok {
			return e
		}
		tx.Warn(errs.UnusableClaims(p, errs.UnusableUnreadable, d.Code))
	case ClaimsCorrupt:
		tx.Warn(errs.UnusableClaims(p, errs.UnusableCorrupt, ""))
	case ClaimsUnsupported:
		tx.Warn(errs.UnusableClaims(p, errs.UnusableUnsupportedFormat, ""))
	}
	return nil
}

// Claim is id's entry in the claims file as the transaction holds it; ok is
// false when it has none, and for every ID when the file is not usable. A
// caller loads the file first, with Claims or ClaimsForRead, so that an
// unusable one is its error or its warning: Claim loads it only as a last
// resort, and then says nothing about its state.
func (tx *Tx) Claim(id model.ID) (model.ClaimEntry, bool) {
	cr := tx.loadClaims()
	if cr.state != ClaimsOK {
		return model.ClaimEntry{}, false
	}
	e, ok := cr.file.Claims[id]
	return e, ok
}

// ClaimIDs is the IDs the claims file holds an entry for, ascending.
func (tx *Tx) ClaimIDs() []model.ID {
	cr := tx.loadClaims()
	ids := make([]model.ID, 0, len(cr.file.Claims))
	for id := range cr.file.Claims {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// ClaimsState is the claims file's outcome, and the error an operation that
// needs the file would fail with, nil when it is ok, missing, or another
// root's. dir reports a directory in the file's place, which no write can
// replace.
func (tx *Tx) ClaimsState() (state ClaimsState, dir bool, e *errs.Error) {
	cr := tx.loadClaims()
	return cr.state, cr.dir, claimsError(tx.env, cr)
}

// ClaimsPath is the claims file's path, which is outside the root.
func (tx *Tx) ClaimsPath() string { return tx.env.ClaimsPath() }

// PutClaim records entry as id's claim, replacing the claims file whole. The
// caller has loaded the file with Claims, and it is usable.
func (tx *Tx) PutClaim(id model.ID, entry model.ClaimEntry) *errs.Error {
	claims, e := tx.currentClaims("put a claim")
	if e != nil {
		return e
	}
	claims[id] = entry
	return tx.claimsWriteError(tx.writeClaims(claims))
}

// DropClaims removes the entries of ids from the claims file, in one write,
// and returns the IDs that had one, ascending. With none to remove it writes
// nothing. When the write fails it returns the error with those same IDs,
// whose entries remain. The caller has loaded the file with Claims, and it is
// usable.
func (tx *Tx) DropClaims(ids []model.ID) ([]model.ID, *errs.Error) {
	dropped, e, err := tx.DropClaimsRaw(ids)
	if e == nil && err != nil {
		e = errs.FromOS(tx.env.ClaimsPath(), err)
	}
	return dropped, e
}

// DropClaimsRaw is DropClaims returning a failed write's OS error as it is,
// for a call site that gives it its own meaning (the claims-not-updated
// warning of done, delete, and delete-folder). e is any other failure: a bug.
func (tx *Tx) DropClaimsRaw(ids []model.ID) (dropped []model.ID, e *errs.Error, err error) {
	claims, e := tx.currentClaims("drop claims")
	if e != nil {
		return nil, e, nil
	}
	for _, id := range ids {
		if _, ok := claims[id]; ok {
			delete(claims, id)
			dropped = append(dropped, id)
		}
	}
	if len(dropped) == 0 {
		return nil, nil, nil
	}
	slices.Sort(dropped)
	dropped = slices.Compact(dropped)
	e, err = tx.writeClaims(claims)
	return dropped, e, err
}

// ClaimsCode is err's symbolic OS error name, an OS error on the claims
// file, for a warning; "" for one with no name.
func (tx *Tx) ClaimsCode(err error) (string, *errs.Error) {
	e := errs.FromOS(tx.env.ClaimsPath(), err)
	if d, ok := e.Details.(errs.IODetails); ok {
		return d.Code, nil
	}
	return "", e
}

// ResetClaims replaces the claims file with one naming this root with no
// claims, whatever is in its place: repair's fix for an unusable one.
func (tx *Tx) ResetClaims() *errs.Error {
	return tx.claimsWriteError(tx.writeClaims(map[model.ID]model.ClaimEntry{}))
}

// currentClaims is a copy of this root's entries, to change and write back:
// none when the file is missing or another root's. Changing a file that was
// never loaded, or that is unusable, would overwrite claims no one has read,
// so either is a bug, reported as internal.
func (tx *Tx) currentClaims(what string) (map[model.ID]model.ClaimEntry, *errs.Error) {
	if !tx.claimsRead {
		return nil, errs.Internal(what + " before loading " + ClaimsName)
	}
	if e := claimsError(tx.env, tx.claims); e != nil {
		return nil, errs.Internal(what + " in an unusable " + ClaimsName)
	}
	out := map[model.ID]model.ClaimEntry{}
	for id, e := range tx.claims.file.Claims {
		out[id] = e
	}
	return out, nil
}

// claimsWriteError is writeClaims' failure as an error: an OS error is io, on
// the claims file.
func (tx *Tx) claimsWriteError(e *errs.Error, err error) *errs.Error {
	if e == nil && err != nil {
		return errs.FromOS(tx.env.ClaimsPath(), err)
	}
	return e
}

// writeClaims publishes the claims file naming this root with claims, as the
// state file is published (publishBesideConfig), over whatever is at its
// path. The transaction then holds what it wrote. err is the OS error of a
// failed write, with nothing published; e is any other failure.
func (tx *Tx) writeClaims(claims map[model.ID]model.ClaimEntry) (e *errs.Error, err error) {
	if e := tx.mustWrite("write " + ClaimsName); e != nil {
		return e, nil
	}
	file := model.ClaimsFile{Schema: model.ClaimsSchema, Root: tx.rootPath, Claims: claims}
	data, encErr := file.Encode()
	if encErr != nil {
		return errs.Internal("encode " + ClaimsName + ": " + encErr.Error()), nil
	}
	if _, err := publishBesideConfig(tx.env, ClaimsName, data); err != nil {
		return nil, err
	}
	tx.claims, tx.claimsRead = claimsResult{state: ClaimsOK, file: file}, true
	return nil, nil
}
