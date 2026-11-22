package lima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCreatingLimactl reports the instance as missing, records every subcommand it
// is given, and succeeds. It lets the create path be tested without a VM.
func fakeCreatingLimactl(t *testing.T) (binary, logPath string) {
	t.Helper()
	dir := t.TempDir()
	binary = filepath.Join(dir, "limactl")
	logPath = filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
echo "$1" >> "` + logPath + `"
case "$1" in
  list) exit 1 ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary, logPath
}

// A first run must start the instance it creates. `limactl create` leaves the
// instance stopped, so without an explicit start the command that follows fails
// with "instance is stopped" — which is what happened on the first real run.
func TestCreateStartsTheInstance(t *testing.T) {
	binary, logPath := fakeCreatingLimactl(t)
	g := Guest{
		Name:    "crew-test",
		Mounts:  []Mount{{Location: "/tmp/repo", MountPoint: "/tmp/repo"}},
		Limactl: binary,
	}
	if err := g.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Fields(string(data))
	if len(calls) < 2 {
		t.Fatalf("expected create and start, got %v", calls)
	}
	if calls[0] != "list" {
		t.Fatalf("first call should be list, got %q", calls[0])
	}
	if calls[1] != "create" {
		t.Fatalf("second call should be create, got %q", calls[1])
	}
	if calls[2] != "start" {
		t.Fatalf("create must be followed by start, got %q", calls[2])
	}
}
