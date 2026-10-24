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
// once it is given, what each word after it does: nothing, unless the
// command takes more words.
func (c *Command) subjectCompletion(env *Env) cobra.CompletionFunc {
	var first cobra.CompletionFunc
	switch s := c.Subject; {
	case s == nil:
		first = fixed(noFiles)
	case s.Kind == Path:
		first = fixed(fileNames)
	case s.Complete != nil:
		first = completion(env, s.Complete)
	default:
		first = fixed(noFiles)
	}
	rest := fixed(noFiles)
	if c.Rest != nil && c.Rest.Complete != nil {
		rest = completion(env, c.Rest.Complete)
	}
	return func(cmd *cobra.Command, args []string, word string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return rest(cmd, args, word)
		}
		return first(cmd, args, word)
	}
}

// lenient is an option's value while a line is completed: what the
// command would refuse on the rest of the line doesn't stop completion.
type lenient struct{ pflag.Value }

func (v lenient) Set(text string) error {
	v.Value.Set(text)
	return nil
}
