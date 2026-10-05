package pick

import (
	"fmt"
	"strings"

	"github.com/phansen314/koan/internal/errs"
)

// matchAtOnce matches the query against lines headlessly, with fzf
// --filter and the options the picker gets, in the same order, for
// --select-one and --exit-zero (pick-spec.md, Selecting at once). It
// returns the keys of the lines matched; fzf --filter's exit 1 is no match,
// and any status but 0 and 1 is fzf-failed.
func matchAtOnce(env Env, fzf string, pk picker, lines []string) ([]string, *errs.Error) {
	var stdin []byte
	if len(lines) > 0 {
		stdin = []byte(strings.Join(lines, "\n") + "\n")
	}
	out, status, err := env.Sys.Filter(fzf, append(pk.args(), "--filter", pk.query), env.Sys.Environ(), stdin)
	switch {
	case err != nil:
		return nil, unavailable(fmt.Sprintf("fzf --filter failed: %v", err), UnavailableDetails{Reason: FzfFailed, Actions: []any{}})
	case status == 1:
		return []string{}, nil
	case status != 0:
		return nil, unavailable(fmt.Sprintf("fzf --filter exited with status %d; any message from fzf is above, on the terminal", status),
			UnavailableDetails{Reason: FzfFailed, Status: &status, Actions: []any{}})
	}
	keys := []string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if k, _, _ := strings.Cut(line, lineDelimiter); k != "" {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// decideAtOnce is whether pick finishes without the picker, and with what
// selection: the one match with --select-one, none with --exit-zero.
func decideAtOnce(matched []string, selectOne, exitZero bool) ([]string, bool) {
	switch {
	case selectOne && len(matched) == 1:
		return matched, true
	case exitZero && len(matched) == 0:
		return []string{}, true
	}
	return nil, false
}
