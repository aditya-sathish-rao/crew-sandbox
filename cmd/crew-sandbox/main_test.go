package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

// mountJSON renders a mount the way lima reports it, with the path resolved. The CLI
// mounts the resolved directory, so an unresolved temp path would never match.
func mountJSON(t *testing.T, dir string, writable bool) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return `{"location":"` + real + `","mountPoint":"` + real + `","writable":` + strconv.FormatBool(writable) + `}`
}

func TestRunRefusesAWritableMountItDidNotAskFor(t *testing.T) {
	// An instance created earlier with --writable keeps that mount for its whole
	// life. A later run that asks for no workspace must be refused rather than run
	// against a writable host mount the operator believes is not attached.
	dir := t.TempDir()
	fakeLimactl(t, mountJSON(t, dir, true))

	err := run([]string{"run", "--", "true"})
	if !errors.Is(err, lima.ErrMountsNotApplied) {
		t.Fatalf("run accepted an unexpected writable mount: %v", err)
	}
}

func TestRunRefusesToDowngradeAWritableMountToReadOnly(t *testing.T) {
	// A read-only request against an existing writable mount is not satisfied: the
	// guest would keep write access the operator asked it not to have.
	dir := t.TempDir()
	fakeLimactl(t, mountJSON(t, dir, true))

	err := run([]string{"run", "--workspace", dir, "--", "true"})
	if !errors.Is(err, lima.ErrMountsNotApplied) {
		t.Fatalf("run accepted a writable mount for a read-only request: %v", err)
	}
}

func TestRunAcceptsTheMountItAlreadyHas(t *testing.T) {
	// The same workspace on the second and later runs must work, not fail: the
	// equality check is about what is attached, not about freshness.
	dir := t.TempDir()
	fakeLimactl(t, mountJSON(t, dir, true))

	if err := run([]string{"run", "--workspace", dir, "--writable", "--", "true"}); err != nil {
		t.Fatalf("run rejected a mount the instance already has: %v", err)
	}
}

func TestRunForegroundAsksPolitelyBeforeKilling(t *testing.T) {
	// The broker and the proxy both release credentials when asked to stop - the
	// broker's tokens exist only in its memory - so a cancelled service has to get
	// SIGTERM and a grace period, not exec's default SIGKILL. The child here traps
	// TERM and exits 42: seeing 42 instead of a signal death is the proof.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c",
		`trap 'exit 42' TERM; while :; do sleep 0.02; done`)
	cmd.WaitDelay = 2 * time.Second

	done := make(chan error, 1)
	go func() { done <- runForeground(cmd) }()
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		var exit exitError
		if !errors.As(err, &exit) {
			t.Fatalf("runForeground returned %v, want an exit status", err)
		}
		if exit.code != 42 {
			t.Fatalf("exit code = %d, want 42: the SIGTERM handler did not run", exit.code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runForeground did not return after the context was cancelled")
	}
}

func TestWorkspaceRefusals(t *testing.T) {
	// A workspace is the one thing the guest can see of the host, so the paths that
	// must never be one are refused by name rather than left to the operator.
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{".ssh", ".aws", "project"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store := filepath.Join(home, "lima-store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIMA_HOME", store)

	cases := []struct{ name, workspace string }{
		{"the filesystem root", "/"},
		{"the home directory", home},
		{"the SSH directory", filepath.Join(home, ".ssh")},
		{"the AWS directory", filepath.Join(home, ".aws")},
		{"a directory above home, which contains .ssh", filepath.Dir(home)},
		{"Lima's instance store", store},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := run([]string{"run", "--workspace", c.workspace, "--", "true"})
			if err == nil {
				t.Fatalf("workspace %s was accepted", c.workspace)
			}
			if !strings.Contains(err.Error(), "workspace") {
				t.Fatalf("unhelpful refusal for %s: %v", c.workspace, err)
			}
		})
	}
}

func TestWorkspaceRefusesASymlinkToHome(t *testing.T) {
	// The textual path is innocent; the resolved one is not. This is why the check
	// runs after EvalSymlinks.
	home := t.TempDir()
	t.Setenv("HOME", home)
	link := filepath.Join(t.TempDir(), "innocent")
	if err := os.Symlink(home, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := run([]string{"run", "--workspace", link, "--", "true"}); err == nil {
		t.Fatal("a symlink that resolves to the home directory was accepted")
	}
}

func TestWorkspaceMountUsesTheResolvedPath(t *testing.T) {
	// The mount has to be the directory that was checked, not a link that could be
	// repointed after the check. The fake reports the resolved path as the mount it
	// already has, so this only passes if the resolved path is what gets mounted.
	home := t.TempDir()
	t.Setenv("HOME", home)
	real := filepath.Join(home, "work")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "work-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	fakeLimactl(t, mountJSON(t, real, false))

	if err := run([]string{"run", "--workspace", link, "--", "true"}); err != nil {
		t.Fatalf("run with a symlinked workspace: %v", err)
	}
}
