package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aditya-sathish-rao/crew-sandbox/internal/lima"
)

// fakeLimactl puts a limactl on PATH that answers the two `list` forms the
// lifecycle uses, records every invocation, and runs the guest command locally so
// the CLI's contract is observable without a VM.
//
// It drops the `sudo -n -u agent` wrapper before executing, because the test host
// has no `agent` user. TestRunRunsAsTheAgentUser asserts on the recorded argv
// instead, which is where the wrapper actually is.
func fakeLimactl(t *testing.T, mounts string) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "argv.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FM_FAKE_LOG"
case "$1" in
  list)
    case "$3" in
      --json) printf '%s\n' '{"config":{"mounts":[` + mounts + `]}}' ;;
      *) printf '%s\n' "Running" ;;
    esac
    exit 0
    ;;
  shell)
    while [ "$#" -gt 0 ]; do case "$1" in --) shift; break ;; *) shift ;; esac; done
    if [ "$1" = "sudo" ]; then
      while [ "$#" -gt 0 ]; do case "$1" in --) shift; break ;; *) shift ;; esac; done
    fi
    exec "$@"
    ;;
  *)
    echo "fake limactl: unexpected subcommand $1" >&2
    exit 1
    ;;
esac
`
	path := filepath.Join(dir, "limactl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FM_FAKE_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func TestRunReturnsTheGuestExitStatus(t *testing.T) {
	// The CLI's stated contract is that `run` returns the command's exit status. It
	// only did that if the error stayed identifiable, which it did not while the
	// lima package formatted failures as strings: every failure exited 1.
	fakeLimactl(t, "")

	err := run([]string{"run", "--", "sh", "-c", "exit 7"})
	var exit exitError
	if !errors.As(err, &exit) {
		t.Fatalf("run did not return an exit status: %v", err)
	}
	if exit.code != 7 {
		t.Fatalf("exit code = %d, want 7", exit.code)
	}
}

func TestRunSucceedsWhenTheGuestCommandSucceeds(t *testing.T) {
	fakeLimactl(t, "")
	if err := run([]string{"run", "--", "true"}); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestRunRunsAsTheAgentUser(t *testing.T) {
	// The Lima user is the administration account and keeps passwordless sudo.
	// Running the operator's command as that account puts untrusted work one `sudo`
	// away from the guest's root, which is the boundary this tool exists to hold.
	logPath := fakeLimactl(t, "")
	if err := run([]string{"run", "--", "true"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "sudo -n -u agent -H --") {
		t.Fatalf("guest command was not wrapped as the agent user:\n%s", logged)
	}
}

func TestRunRefusesAWritableMountItDidNotAskFor(t *testing.T) {
	// An instance created earlier with --writable keeps that mount for its whole
	// life. A later run that asks for no workspace must be refused rather than run
	// against a writable host mount the operator believes is not attached.
	dir := t.TempDir()
	fakeLimactl(t, `{"location":"`+dir+`","mountPoint":"`+dir+`","writable":true}`)

	err := run([]string{"run", "--", "true"})
	if !errors.Is(err, lima.ErrMountsNotApplied) {
		t.Fatalf("run accepted an unexpected writable mount: %v", err)
	}
}

func TestRunRefusesToDowngradeAWritableMountToReadOnly(t *testing.T) {
	// A read-only request against an existing writable mount is not satisfied: the
	// guest would keep write access the operator asked it not to have.
	dir := t.TempDir()
	fakeLimactl(t, `{"location":"`+dir+`","mountPoint":"`+dir+`","writable":true}`)

	err := run([]string{"run", "--workspace", dir, "--", "true"})
	if !errors.Is(err, lima.ErrMountsNotApplied) {
		t.Fatalf("run accepted a writable mount for a read-only request: %v", err)
	}
}

func TestRunAcceptsTheMountItAlreadyHas(t *testing.T) {
	// The same workspace on the second and later runs must work, not fail: the
	// equality check is about what is attached, not about freshness.
	dir := t.TempDir()
	fakeLimactl(t, `{"location":"`+dir+`","mountPoint":"`+dir+`","writable":true}`)

	if err := run([]string{"run", "--workspace", dir, "--writable", "--", "true"}); err != nil {
		t.Fatalf("run rejected a mount the instance already has: %v", err)
	}
}
