package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// parseFlags parses args with fs, printing the flag help to stderr on -h or a mistake.
// ok is false when the command must stop; code is then its exit code.
func parseFlags(fs *flag.FlagSet, args []string, stderr io.Writer) (code int, ok bool) {
	fs.SetOutput(stderr)
	err := fs.Parse(args)
	switch {
	case err == nil:
		return exitOK, true
	case errors.Is(err, flag.ErrHelp):
		return exitOK, false
	}
	return exitUsage, false
}

// fail prints a runtime error and returns the runtime exit code.
func fail(stderr io.Writer, command string, err error) int {
	_, _ = fmt.Fprintf(stderr, "crimson-api %s: %v\n", command, err)
	return exitRuntime
}

// usageFail prints a usage mistake and returns the usage exit code.
func usageFail(stderr io.Writer, command, msg string) int {
	_, _ = fmt.Fprintf(stderr, "crimson-api %s: %s\n", command, msg)
	return exitUsage
}

// fmtLine writes one plain guidance line to stderr.
func fmtLine(io IO, msg string) { _, _ = fmt.Fprintln(io.Stderr, msg) }
