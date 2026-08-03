package main

import (
	"io"
	"os"
	"testing"
)

// captureOutput redirects stdout and stderr for the duration of fn and
// returns what each collected. The help paths under test write only to
// stdout, and the no-arg error paths only to stderr, so tests can check
// both without caring which one a given branch happens to use.
func captureOutput(t *testing.T, fn func() int) (stdout, stderr string, code int) {
	t.Helper()

	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = outW
	os.Stderr = errW
	defer func() {
		os.Stdout = origOut
		os.Stderr = origErr
	}()

	code = fn()

	outW.Close()
	errW.Close()
	outBytes, _ := io.ReadAll(outR)
	errBytes, _ := io.ReadAll(errR)
	return string(outBytes), string(errBytes), code
}

// TestHelpFlagsPrintUsageAndExitZero covers the bug in issue #8: each of
// these subcommands used to either build something from the literal text
// "--help"/"-h" (open, project) or fall into the unknown-flag branch and
// exit 2 (setups, handoff). All four now recognise a help flag before doing
// anything else and print usage to stdout with exit 0.
func TestHelpFlagsPrintUsageAndExitZero(t *testing.T) {
	tests := []struct {
		name  string
		run   func([]string) int
		args  []string
		usage string
	}{
		{"open --help", runOpen, []string{"--help"}, openUsage},
		{"open -h", runOpen, []string{"-h"}, openUsage},
		{"project --help", runProject, []string{"--help"}, projectUsage},
		{"project -h", runProject, []string{"-h"}, projectUsage},
		{"setups --help", runSetups, []string{"--help"}, setupsUsage},
		{"handoff --help", runHandoff, []string{"--help"}, handoffUsage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := captureOutput(t, func() int { return tt.run(tt.args) })
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
			}
			if stderr != "" {
				t.Fatalf("unexpected stderr: %q", stderr)
			}
			if got := trimNL(stdout); got != tt.usage {
				t.Fatalf("stdout = %q, want %q", got, tt.usage)
			}
		})
	}
}

// TestOpenHelpFlagInLaterPosition covers `open somepr --help`: the flag loop
// itself (not just the args[0] check) has to recognise -h/--help.
func TestOpenHelpFlagInLaterPosition(t *testing.T) {
	stdout, stderr, code := captureOutput(t, func() int {
		return runOpen([]string{"somepr", "--help"})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if got := trimNL(stdout); got != openUsage {
		t.Fatalf("stdout = %q, want %q", got, openUsage)
	}
}

// TestProjectHelpFlagInLaterPosition is the same case for "project".
func TestProjectHelpFlagInLaterPosition(t *testing.T) {
	stdout, stderr, code := captureOutput(t, func() int {
		return runProject([]string{"somedir", "--help"})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if got := trimNL(stdout); got != projectUsage {
		t.Fatalf("stdout = %q, want %q", got, projectUsage)
	}
}

// TestOpenHelpWordIsNotAHelpFlag is the negative case the fix has to keep
// true: "open" takes arbitrary text as its Space name by design, so the bare
// word "help" is a legitimate input, not a request for usage. It must not
// take the help branch (exit 0, usage on stdout) -- it has to fall through
// to the real body of runOpen.
//
// There is no Herdr socket in a test environment (HERDR_SOCKET_PATH unset),
// so the real body fails at herdr.New() with exit 1 before anything could be
// created -- which is exactly the failure signature that proves the help
// branch was not taken.
func TestOpenHelpWordIsNotAHelpFlag(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "")

	stdout, stderr, code := captureOutput(t, func() int {
		return runOpen([]string{"help"})
	})
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero: \"help\" must not be treated as -h/--help (stdout: %q)", stdout)
	}
	if trimNL(stdout) == openUsage {
		t.Fatalf("stdout printed the usage string; \"help\" was wrongly treated as a help flag (stderr: %q)", stderr)
	}
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
