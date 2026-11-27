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

// fakeSSH puts an ssh on PATH that records its argv, so the attach command's
// contract is observable without a VM.
func fakeSSH(t *testing.T) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "ssh.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$FM_FAKE_SSH_LOG\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FM_FAKE_SSH_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// fakeLimaHome creates the instance directory and the ssh config Lima writes, so
// attach's existence check passes.
func fakeLimaHome(t *testing.T, name string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := "Host lima-" + name + "\n  User crew\n  Hostname 127.0.0.1\n  Port 60022\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh.config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIMA_HOME", home)
}

func TestAttachOpensAPtyAsTheAgentUser(t *testing.T) {
	// run forces --tty=false, so it cannot carry a full-screen program. attach is the
	// only way in, and it has to land as the unprivileged account rather than the
	// Lima admin account.
	fakeLimactl(t, "")
	fakeLimaHome(t, "crew-sandbox")
	logPath := fakeSSH(t)

	if err := run([]string{"attach"}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(logged))
	for _, want := range []string{"-tt", "lima-crew-sandbox", "sudo -n -u agent -H --"} {
		if !strings.Contains(line, want) {
			t.Errorf("ssh argv is missing %q: %s", want, line)
		}
	}
}

func TestAttachPassesTheCommandThrough(t *testing.T) {
	// `attach -- herdr` is the documented way to reach a full-screen program in the
	// guest, so the command has to survive into the remote script.
	fakeLimactl(t, "")
	fakeLimaHome(t, "crew-sandbox")
	logPath := fakeSSH(t)

	if err := run([]string{"attach", "--", "herdr", "--remote", "x"}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "herdr --remote x") {
		t.Fatalf("the command did not reach the guest: %s", logged)
	}
}

func TestAttachReturnsTheSessionExitStatus(t *testing.T) {
	// A script that runs `crew-sandbox attach -- cmd` has to see cmd's status, the
	// same way it would from run.
	fakeLimactl(t, "")
	fakeLimaHome(t, "crew-sandbox")
	dir := t.TempDir()
	script := "#!/bin/sh\nexit 5\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := run([]string{"attach"})
	var exit exitError
	if !errors.As(err, &exit) {
		t.Fatalf("attach did not return an exit status: %v", err)
	}
	if exit.code != 5 {
		t.Fatalf("exit code = %d, want 5", exit.code)
	}
}

func TestAttachStartsAStoppedInstance(t *testing.T) {
	// Attach is the only way into the VM, so it must not require a separate `up`
	// first. It also must not apply run's mount check: attach runs nothing against a
	// mount, and an instance created with a workspace has to stay attachable.
	fakeLimaHome(t, "crew-sandbox")
	fakeSSH(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FM_FAKE_LOG"
case "$1" in
  list)
    case "$3" in
      --json) printf '%s\n' '{"config":{"mounts":[{"location":"/tmp/repo","mountPoint":"/tmp/repo","writable":true}]}}' ;;
      *) printf '%s\n' "Stopped" ;;
    esac
    ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "limactl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FM_FAKE_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := run([]string{"attach"}); err != nil {
		t.Fatalf("attach on a stopped instance with a mount: %v", err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "start --tty=false crew-sandbox") {
		t.Fatalf("attach did not start the stopped instance: %s", logged)
	}
}

// fakeProxyNode puts a `node` on PATH that records the environment it was given and
// exits, so the proxy's token plumbing is observable without starting a listener.
func fakeProxyNode(t *testing.T) (envPath string) {
	t.Helper()
	dir := t.TempDir()
	envPath = filepath.Join(dir, "env.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$CREW_MODEL_TOKEN\" > \"$FM_FAKE_ENV_LOG\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FM_FAKE_ENV_LOG", envPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return envPath
}

func TestProxyReadsTheModelTokenFromAFile(t *testing.T) {
	// A fleet that runs on the host has no guest to read /etc/crew-model-token from,
	// so the operator names a file they own. The token has to reach the gateway
	// process, or every request is refused with 401.
	envPath := fakeProxyNode(t)
	tokenFile := filepath.Join(t.TempDir(), "model-token")
	if err := os.WriteFile(tokenFile, []byte("host-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{"proxy", "--model-token-file", tokenFile}); err != nil {
		t.Fatalf("proxy: %v", err)
	}
	logged, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(logged)); got != "host-token" {
		t.Fatalf("CREW_MODEL_TOKEN = %q, want %q", got, "host-token")
	}
}

func TestProxyRefusesAnUnreadableModelTokenFile(t *testing.T) {
	// A named file that cannot be read is a configuration error, not a guest that
	// happens to be down: starting anyway would serve a gateway that refuses every
	// request while reporting healthy.
	fakeProxyNode(t)
	missing := filepath.Join(t.TempDir(), "absent")

	err := run([]string{"proxy", "--model-token-file", missing})
	if err == nil {
		t.Fatal("proxy started with an unreadable model token file")
	}
	if !strings.Contains(err.Error(), "model token file") {
		t.Fatalf("error does not name the token file: %v", err)
	}
}

func TestProxyRefusesAnEmptyModelTokenFile(t *testing.T) {
	// An empty file is the same failure as a missing one: the gateway would come up
	// with no token to compare against.
	fakeProxyNode(t)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	err := run([]string{"proxy", "--model-token-file", empty})
	if err == nil {
		t.Fatal("proxy started with an empty model token file")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error does not say the file is empty: %v", err)
	}
}
