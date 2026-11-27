package lima

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMountOverrideReadOnly(t *testing.T) {
	got := mountOverride(Mount{Location: "/tmp/repo", MountPoint: "/tmp/repo"})
	want := `.mounts += [{"location": "/tmp/repo", "mountPoint": "/tmp/repo", "writable": false}]`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMountOverrideWritable(t *testing.T) {
	got := mountOverride(Mount{Location: "/tmp/repo", MountPoint: "/tmp/repo", Writable: true})
	if !strings.Contains(got, `"writable": true`) {
		t.Fatalf("writable mount not rendered as writable: %s", got)
	}
}

func TestMountOverrideEscapesQuotes(t *testing.T) {
	// A quote in a path must be escaped, or the yq expression would end the
	// string early and the mount would be silently dropped or misparsed.
	got := mountOverride(Mount{Location: `/tmp/a"b`, MountPoint: `/tmp/a"b`})
	if !strings.Contains(got, `a\"b`) {
		t.Fatalf("quote in path was not escaped: %s", got)
	}
}

func TestEmbeddedDefinitionHasNoMounts(t *testing.T) {
	// The boundary depends on the default definition mounting nothing. A mount
	// added to the embedded file would silently widen every guest.
	if !strings.Contains(string(definition), "mounts: []") {
		t.Fatal("embedded lima.yaml no longer declares `mounts: []`")
	}
}

func TestEmbeddedDefinitionHasNoPortForwards(t *testing.T) {
	// The definition must keep the ignore rule that closes every forwarded port.
	if !strings.Contains(string(definition), "ignore: true") {
		t.Fatal("embedded lima.yaml no longer ignores port forwards")
	}
}

func TestMountsSatisfiedRejectsExtraMount(t *testing.T) {
	// An instance created earlier with a writable workspace carries that mount for
	// its whole life. A later run that asks for no workspace must not be considered
	// satisfied by it: the guest would still hold a writable host mount the operator
	// believes is gone.
	applied := []Mount{{Location: "/tmp/repo", MountPoint: "/tmp/repo", Writable: true}}
	if mountsSatisfied(nil, applied) {
		t.Fatal("an extra writable mount satisfied a run that asked for no mounts")
	}
}

func TestMountsSatisfiedIsExact(t *testing.T) {
	want := []Mount{{Location: "/tmp/repo", MountPoint: "/tmp/repo"}}
	cases := []struct {
		name string
		have []Mount
		ok   bool
	}{
		{"identical", want, true},
		{"writable instead of read-only", []Mount{{Location: "/tmp/repo", MountPoint: "/tmp/repo", Writable: true}}, false},
		{"different mount point", []Mount{{Location: "/tmp/repo", MountPoint: "/mnt/repo"}}, false},
		{"missing", nil, false},
		{"extra alongside the wanted one", append(append([]Mount{}, want...), Mount{Location: "/tmp/other", MountPoint: "/tmp/other"}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mountsSatisfied(want, c.have); got != c.ok {
				t.Fatalf("mountsSatisfied = %v, want %v", got, c.ok)
			}
		})
	}
}

func TestAsAgentRunsAsTheUnprivilegedUser(t *testing.T) {
	// The Lima user is the administration account and keeps passwordless sudo, so
	// untrusted work must be wrapped as the unprivileged account or it runs as root
	// equivalent. `-n` matters: it fails closed instead of prompting.
	got := asAgent([]string{"go", "test", "./..."})
	want := []string{"sudo", "-n", "-u", "agent", "-H", "--", "go", "test", "./..."}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("asAgent = %v, want %v", got, want)
	}
}

func TestEveryGuestEntryPointWrapsAsAgent(t *testing.T) {
	// asAgent is unexported and every exported entry point applies it, so no caller can
	// run a guest command as the Lima user by forgetting a wrapper. That is the whole
	// point of baking it in: a boundary, not a convention. If a fourth entry point is
	// ever added, this test is where it shows up as unwrapped.
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv.log")
	path := filepath.Join(dir, "limactl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := Guest{Name: "crew-test", Limactl: path}
	if _, err := g.Run([]string{"id", "-un"}, ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := g.RunIO(context.Background(), []string{"id", "-un"}, "", strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("RunIO: %v", err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two invocations, got %d: %q", len(lines), logged)
	}
	for _, line := range lines {
		if !strings.Contains(line, "sudo -n -u agent -H --") {
			t.Fatalf("a guest command ran without the agent wrapper: %s", line)
		}
	}

	// Attach is the third entry point and does not go through limactl, so it needs its
	// own check. It is the one that matters most: an interactive session as the Lima
	// user would be a root shell one `sudo` away, handed to whoever is at the keyboard.
	sshLog := filepath.Join(dir, "ssh.log")
	sshPath := filepath.Join(dir, "ssh")
	sshScript := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + sshLog + "\nexit 0\n"
	if err := os.WriteFile(sshPath, []byte(sshScript), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "crew-test"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "crew-test", "ssh.config"), []byte("Host lima-crew-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIMA_HOME", home)
	attaching := Guest{Name: "crew-test", SSH: sshPath}
	if err := attaching.Attach(context.Background(), []string{"id", "-un"}, "", strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	sshLogged, err := os.ReadFile(sshLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sshLogged), "sudo -n -u agent -H --") {
		t.Fatalf("attach ran without the agent wrapper: %s", sshLogged)
	}
}

func TestOnlyThisPackageInvokesLimactl(t *testing.T) {
	// The package doc claims this is the only package that knows the VM exists. It is
	// also the only place the agent wrapper is applied, so any other package shelling
	// out to limactl directly would bypass it and run as the administration account.
	// Scan for that rather than trusting the claim.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "graphify-out" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"internal"+string(filepath.Separator)+"lima"+string(filepath.Separator)) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for number, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(strings.ToLower(line), "limactl") {
				t.Errorf("%s:%d invokes limactl outside internal/lima, which is where the agent wrapper lives: %s", path, number+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMountOverrideEscapesBackslash(t *testing.T) {
	// A trailing backslash would escape the closing quote and break out of the YAML
	// scalar, silently changing which directory is mounted.
	got := mountOverride(Mount{Location: `/tmp/a\`, MountPoint: `/tmp/a\`})
	if !strings.Contains(got, `a\\`) {
		t.Fatalf("backslash in path was not escaped: %s", got)
	}
}

// fakeShell writes a limactl that answers `shell` with the given streams and exit
// status, so the Run and RunIO paths can be tested without a VM.
func fakeShell(t *testing.T, stdout, stderr string, code int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "limactl")
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += "printf '%s\\n' '" + stdout + "'\n"
	}
	if stderr != "" {
		script += "printf '%s\\n' '" + stderr + "' >&2\n"
	}
	script += "exit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunKeepsExitStatusReachable(t *testing.T) {
	// The CLI promises `run` returns the command's exit status. That only works if
	// the error stays identifiable as an *exec.ExitError; formatting it as a string
	// made every failure exit 1.
	g := Guest{Name: "crew-test", Limactl: fakeShell(t, "", "boom", 3)}
	_, err := g.Run([]string{"false"}, "")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("exit status is not reachable from %v", err)
	}
	if exitErr.ExitCode() != 3 {
		t.Fatalf("exit code = %d, want 3", exitErr.ExitCode())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("limactl's own explanation was dropped: %v", err)
	}
}

func TestRunIOStreamsTheStreamsSeparately(t *testing.T) {
	// Buffered capture folded stderr into the returned error and dropped it on
	// success. Running a command in the guest is an interactive path: stdout and
	// stderr have to arrive as they are produced, on their own streams.
	g := Guest{Name: "crew-test", Limactl: fakeShell(t, "out", "err", 0)}
	var stdout, stderr bytes.Buffer
	err := g.RunIO(context.Background(), []string{"true"}, "", strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunIO: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "out" {
		t.Errorf("stdout = %q, want out", stdout.String())
	}
	if strings.TrimSpace(stderr.String()) != "err" {
		t.Errorf("stderr = %q, want err (stderr must survive a successful command)", stderr.String())
	}
}

func TestRunIORejectsAnEmptyCommand(t *testing.T) {
	g := Guest{Name: "crew-test", Limactl: fakeShell(t, "", "", 0)}
	if err := g.RunIO(context.Background(), nil, "", nil, nil, nil); err == nil {
		t.Fatal("RunIO accepted an empty argv")
	}
}

// fakeSSH puts an ssh on PATH that records its argv and exits with code, so the
// attach path can be tested without a VM. It also writes the ssh config Lima would
// have written, because Attach refuses to run without one.
func fakeSSH(t *testing.T, code int) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$FM_FAKE_LOG\"\nexit " + string(rune('0'+code)) + "\n"
	path := filepath.Join(dir, "ssh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FM_FAKE_LOG", logPath)
	return path
}

// fakeLimaHome creates the instance directory and the ssh config Lima writes, so
// Attach's existence check passes.
func fakeLimaHome(t *testing.T, name string) string {
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
	return home
}

func TestAttachAllocatesAPtyAndRunsAsAgent(t *testing.T) {
	// The whole point of attach is the pty: run forces --tty=false, so it cannot
	// carry a full-screen program. -tt rather than -t, because -t only asks for a pty
	// when the local side happens to have one.
	fakeLimaHome(t, "crew-test")
	ssh := fakeSSH(t, 0)
	g := Guest{Name: "crew-test", SSH: ssh}
	if err := g.Attach(context.Background(), nil, "", strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	logged, err := os.ReadFile(os.Getenv("FM_FAKE_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(logged))
	for _, want := range []string{"-tt", "-F ", "lima-crew-test", "sudo -n -u agent -H --"} {
		if !strings.Contains(line, want) {
			t.Errorf("ssh argv is missing %q: %s", want, line)
		}
	}
}

func TestAttachUsesLimasSSHConfig(t *testing.T) {
	// The identity file, host key policy and forwarded port all live in the config
	// Lima writes. Re-deriving them here would let attach drift from what Lima
	// actually started.
	home := fakeLimaHome(t, "crew-test")
	ssh := fakeSSH(t, 0)
	g := Guest{Name: "crew-test", SSH: ssh}
	if err := g.Attach(context.Background(), nil, "", strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	logged, err := os.ReadFile(os.Getenv("FM_FAKE_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "crew-test", "ssh.config")
	if !strings.Contains(string(logged), want) {
		t.Fatalf("ssh was not pointed at %s: %s", want, logged)
	}
}

func TestAttachRefusesWithoutAnSSHConfig(t *testing.T) {
	// No config means no instance. Failing with the path is more useful than letting
	// ssh report a host it cannot resolve.
	t.Setenv("LIMA_HOME", t.TempDir())
	ssh := fakeSSH(t, 0)
	g := Guest{Name: "crew-test", SSH: ssh}
	err := g.Attach(context.Background(), nil, "", strings.NewReader(""), io.Discard, io.Discard)
	if err == nil {
		t.Fatal("Attach ran without an ssh config")
	}
	if !strings.Contains(err.Error(), "ssh.config") {
		t.Fatalf("error does not name the missing config: %v", err)
	}
}

func TestAttachKeepsTheExitStatusReachable(t *testing.T) {
	// attach is an interactive session, but its exit status still has to reach the
	// caller: a script that runs `crew-sandbox attach -- cmd` must see cmd's status.
	fakeLimaHome(t, "crew-test")
	ssh := fakeSSH(t, 4)
	g := Guest{Name: "crew-test", SSH: ssh}
	err := g.Attach(context.Background(), nil, "", strings.NewReader(""), io.Discard, io.Discard)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("exit status is not reachable from %v", err)
	}
	if exitErr.ExitCode() != 4 {
		t.Fatalf("exit code = %d, want 4", exitErr.ExitCode())
	}
}

func TestAttachScriptStartsInTheAgentsHome(t *testing.T) {
	// The ssh session starts in the Lima user's home, which AgentUser cannot read. A
	// shell that inherited that directory would start somewhere it has no permission
	// to be, so the script has to cd before it execs anything.
	got := attachScript(nil, "")
	if !strings.HasPrefix(got, `cd "$HOME" && exec `) {
		t.Fatalf("attach script does not start in the agent's home: %s", got)
	}
	if !strings.HasSuffix(got, "bash -l") {
		t.Fatalf("attach with no command is not a login shell: %s", got)
	}
}

func TestAttachScriptQuotesTheCommand(t *testing.T) {
	// The remote command is one string that two shells parse in turn: the guest's
	// login shell, then the `bash -c` the agent wrapper runs. An argument with a space
	// or a quote has to survive both parses as one word, or attach would silently run
	// a different command than the operator typed. Assert the round-trip rather than
	// the literal quoting, because the quoting is an implementation detail and the
	// argv is the contract.
	argv := []string{"echo", "it's here", "a b", "$HOME", "back\\slash"}
	remote := shellJoin(asAgent([]string{"bash", "-c", attachScript(argv, "")}))

	// A fake sudo that prints the argv it was handed, so the whole chain is exercised:
	// the outer shell parses `remote`, sudo receives the words, and the inner
	// `bash -c` parses the script it was given.
	dir := t.TempDir()
	fakeSudo := filepath.Join(dir, "sudo")
	script := "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do case \"$1\" in --) shift; break ;; *) shift ;; esac; done\nexec \"$@\"\n"
	if err := os.WriteFile(fakeSudo, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Replace the inner `bash -c` with printf so the parsed argv is observable.
	remote = strings.Replace(remote, "bash -c", "printf '%s\\n'", 1)

	cmd := exec.Command("sh", "-c", remote)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("parsing the remote command: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	want := []string{`cd "$HOME" && exec bash -lc ` + shellQuote(shellJoin(argv))}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("the guest shell parsed the command into %q, want %q", got, want)
	}
}

func TestAttachScriptHonoursWorkdir(t *testing.T) {
	got := attachScript(nil, "/tmp/my dir")
	if !strings.HasPrefix(got, `cd '/tmp/my dir' && exec `) {
		t.Fatalf("workdir was not used or not quoted: %s", got)
	}
}

func TestSSHHostMatchesLimasNaming(t *testing.T) {
	// Lima derives the Host alias by prefixing "lima-" and replacing dots and
	// underscores with dashes. Using "lima-" + Name would not match for a name with
	// either character, and ssh would fail to resolve the host.
	cases := map[string]string{
		"crew-sandbox": "lima-crew-sandbox",
		"crew.test":    "lima-crew-test",
		"crew_test":    "lima-crew-test",
	}
	for name, want := range cases {
		if got := (Guest{Name: name}).sshHost(); got != want {
			t.Errorf("sshHost(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestStartForAttachSkipsTheMountCheck(t *testing.T) {
	// Attach runs nothing against a mount, so an instance created with a workspace
	// must still be attachable. Refusing to start it would make the only way into the
	// VM depend on how the VM happened to be created.
	dir := t.TempDir()
	logPath := filepath.Join(dir, "argv.log")
	path := filepath.Join(dir, "limactl")
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
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FM_FAKE_LOG", logPath)

	g := Guest{Name: "crew-test", Limactl: path}
	if err := g.StartForAttach(); err != nil {
		t.Fatalf("StartForAttach refused an instance with a mount: %v", err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "start --tty=false crew-test") {
		t.Fatalf("StartForAttach did not start the stopped instance: %s", logged)
	}

	// Start must still refuse: the mount check is what keeps a run from silently
	// going without the workspace it asked for.
	if err := g.Start(); !errors.Is(err, ErrMountsNotApplied) {
		t.Fatalf("Start accepted an instance with an extra mount: %v", err)
	}
}
