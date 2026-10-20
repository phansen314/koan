package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Main runs one command line and gives the exit code. Whatever happens,
// it prints exactly one envelope, unless the line is --help alone.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer, commands []*Command) (code int) {
	var warnings []Warning
	defer func() {
		if r := recover(); r != nil {
			e := NewError(KindInternal, fmt.Sprintf("koan failed: %v", r))
			code = write(stdout, stderr, nil, e, warnings)
		}
	}()

	if len(args) == 1 && args[0] == "--help" {
		io.WriteString(stdout, help(commands))
		return ExitOK
	}
	c, rest, e := lookup(commands, args)
	if e != nil {
		return write(stdout, stderr, nil, e, nil)
	}
	if len(rest) == 1 && rest[0] == "--help" {
		io.WriteString(stdout, c.usage())
		return ExitOK
	}
	l, e := c.parse(rest)
	if e != nil {
		return write(stdout, stderr, nil, e, nil)
	}

	env := &Env{Stdin: stdin}

	in := l.in
	if l.hasInput {
		if in, e = readInput(env, l.input); e != nil {
			return write(stdout, stderr, nil, e, nil)
		}
	} else if e := c.readFiles(env, l); e != nil {
		return write(stdout, stderr, nil, e, nil)
	}
	if e := c.unknownFields(in); e != nil {
		return write(stdout, stderr, nil, e, nil)
	}
	result, warnings, e := c.Run(env, in)
	return write(stdout, stderr, result, e, warnings)
}

// lookup finds the command a line names, by one word or two, matched
// exactly.
func lookup(commands []*Command, args []string) (*Command, []string, *Error) {
	if len(args) == 0 {
		return nil, nil, Usage("", "no command given")
	}
	byName := map[string]*Command{}
	groups := map[string]bool{}
	for _, c := range commands {
		byName[c.Name] = c
		if g, _, ok := strings.Cut(c.Name, " "); ok {
			groups[g] = true
		}
	}
	if groups[args[0]] {
		if len(args) == 1 {
			return nil, nil, Usage(args[0], "no command given after "+args[0])
		}
		if c := byName[args[0]+" "+args[1]]; c != nil {
			return c, args[2:], nil
		}
		return nil, nil, Usage(args[1], "unknown command "+args[0]+" "+args[1])
	}
	if c := byName[args[0]]; c != nil {
		return c, args[1:], nil
	}
	return nil, nil, Usage(args[0], "unknown command "+args[0])
}

// usage is the command's line of usage text.
func (c *Command) usage() string {
	var b strings.Builder
	b.WriteString("usage: koan " + c.Name)
	if s := c.Subject; s != nil {
		if s.Required {
			b.WriteString(" <" + s.Field + ">")
		} else {
			b.WriteString(" [<" + s.Field + ">]")
		}
	}
	for _, o := range c.Options {
		switch o.Kind {
		case Bool:
			b.WriteString(" [" + o.Name() + "]")
		case List:
			b.WriteString(" [" + o.Name() + " <a,b,...>]")
		default:
			b.WriteString(" [" + o.Name() + " <value>]")
		}
	}
	for _, f := range c.Files {
		b.WriteString(" [" + f.Name + " <file|->]")
	}
	b.WriteString("\n       koan " + c.Name + " -i <file|->\n")
	return b.String()
}

func help(commands []*Command) string {
	var names []string
	for _, c := range commands {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return "usage: koan <command> [<subject>] [<options>]\n       koan <command> -i <file|->\n\ncommands:\n  " +
		strings.Join(names, "\n  ") + "\n\nkoan <command> --help shows a command's options.\n"
}
