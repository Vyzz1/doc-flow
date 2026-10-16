package ocr

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// helperMarker separates the test runner's own flags from the mode this
// process should act out. It carries no dashes on purpose: flag parsing
// stops at the first argument that is not a flag, so the marker and the mode
// reach os.Args untouched. Passing the mode as an argument rather than an
// environment variable is what lets these tests call the real runCommand
// instead of a copy of it.
const helperMarker = "docflow-helper"

// TestHelperProcess is not a test. Re-executing the test binary is the
// portable way to get a subprocess that behaves exactly as a test needs,
// without depending on which commands the machine happens to have.
func TestHelperProcess(t *testing.T) {
	index := slices.Index(os.Args, helperMarker)
	if index < 0 || index+1 >= len(os.Args) {
		t.Skip("not a helper invocation")
	}
	switch os.Args[index+1] {
	case "ok":
		// A helper that cannot write its own output has nothing useful to
		// tell the parent, so it fails loudly instead.
		if _, err := fmt.Fprint(os.Stdout, "engine output"); err != nil {
			os.Exit(2)
		}
	case "fail":
		if _, err := fmt.Fprint(os.Stderr, "Error in pixReadStream: Pdf reading is not supported"); err != nil {
			os.Exit(2)
		}
		os.Exit(1)
	case "slow":
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

// helperArgs re-enters TestHelperProcess in one mode.
func helperArgs(mode string) []string {
	return []string{"-test.run=^TestHelperProcess$", helperMarker, mode}
}

func TestRunCommandReturnsOutput(t *testing.T) {
	out, err := runCommand(context.Background(), os.Args[0], helperArgs("ok")...)
	if err != nil {
		t.Fatalf("runCommand() error = %v", err)
	}
	if !strings.Contains(string(out), "engine output") {
		t.Errorf("output = %q, want the subprocess's stdout", out)
	}
}

// The engine writes its reason to stderr, which exec discards by default.
// Losing it would leave a bare "exit status 1" in CloudWatch.
func TestRunCommandKeepsStderr(t *testing.T) {
	_, err := runCommand(context.Background(), os.Args[0], helperArgs("fail")...)
	if err == nil {
		t.Fatal("runCommand() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "Pdf reading is not supported") {
		t.Errorf("error = %v, want it to carry the engine's stderr", err)
	}
}

// A slow document must not outlive the Lambda's deadline.
func TestRunCommandRespectsTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := runCommand(ctx, os.Args[0], helperArgs("slow")...); err == nil {
		t.Fatal("runCommand() = nil for a cancelled context, want an error")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("runCommand() took %s, so the deadline was not honoured", elapsed)
	}
}

func TestRunCommandReportsAMissingBinary(t *testing.T) {
	if _, err := runCommand(context.Background(), "docflow-no-such-binary"); err == nil {
		t.Error("runCommand() = nil for a missing binary, want an error")
	}
}
