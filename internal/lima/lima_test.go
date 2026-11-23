package lima

import (
	"bytes"
	"context"
	"errors"
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
	got := AsAgent([]string{"go", "test", "./..."})
	want := []string{"sudo", "-n", "-u", "agent", "-H", "--", "go", "test", "./..."}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("AsAgent = %v, want %v", got, want)
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
