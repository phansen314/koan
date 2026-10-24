package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Candidate is one thing an argument completes to: the text the shell
// puts in, and what it is, for a shell that shows it.
type Candidate struct {
	Text        string
	Description string
}

// Completer gives the candidates for the word being completed, in the
// order the shell is to keep, and whether the shell adds no space after
// the one it puts in. It offers nothing on any failure.
type Completer func(env *Env, word string) (cands []Candidate, noSpace bool)

// maxCandidates is the most a Tab offers; past it, a hint instead (spec
// Completing references, Too many).
const maxCandidates = 100

// noFiles is the directive for a value that offers nothing.
const noFiles = cobra.ShellCompDirectiveNoFileComp

// fileNames is the directive for a file, which the shell completes.
const fileNames = cobra.ShellCompDirectiveDefault

// completion is cobra's completion function for a Completer. Only a
// list's first item completes, so a word holding a comma offers nothing.
func completion(env *Env, complete Completer) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, word string) ([]cobra.Completion, cobra.ShellCompDirective) {
		directive := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
		if strings.Contains(word, ",") {
			return nil, directive
		}
		cands, noSpace := complete(env, word)
		if noSpace {
			directive |= cobra.ShellCompDirectiveNoSpace
		}
		if len(cands) > maxCandidates {
			return cobra.AppendActiveHelp(nil, fmt.Sprintf("%d match: type more, or use koan pick", len(cands))), directive
		}
		out := make([]cobra.Completion, len(cands))
		for i, c := range cands {
			out[i] = cobra.CompletionWithDesc(c.Text, c.Description)
		}
		return out, directive
	}
}

// fixed is a completion function that offers nothing, with directive.
func fixed(directive cobra.ShellCompDirective) cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return nil, directive
	}
}

// subjectCompletion is what the command's subject completes to, and
// once it is given, what each word after it does: the object's, or
// nothing for a command that takes none.
func (c *Command) subjectCompletion(env *Env) cobra.CompletionFunc {
	first, rest := c.Subject.completion(env), c.Object.completion(env)
	// Past one subject and one object, a word is extra.
	one := func(o *Option) bool { return o != nil && o.Kind != List }
	return func(cmd *cobra.Command, args []string, word string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 1 && one(c.Subject) && one(c.Object) {
			return fixed(noFiles)(cmd, args, word)
		}
		if len(args) > 0 {
			return rest(cmd, args, word)
		}
		return first(cmd, args, word)
	}
}

// completion is what a position completes to: files for a path, the
// references its Completer gives, or nothing.
func (o *Option) completion(env *Env) cobra.CompletionFunc {
	switch {
	case o == nil:
		return fixed(noFiles)
	case o.Kind == Path:
		return fixed(fileNames)
	case o.Complete != nil:
		return completion(env, o.Complete)
	}
	return fixed(noFiles)
}

// lenient is an option's value while a line is completed: what the
// command would refuse on the rest of the line doesn't stop completion.
type lenient struct{ pflag.Value }

func (v lenient) Set(text string) error {
	v.Value.Set(text)
	return nil
}
