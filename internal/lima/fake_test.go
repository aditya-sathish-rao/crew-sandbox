package lima

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeLimactl writes a shell script that answers `list --format {{.Status}}` with
// the given status and `list --json` with the given mounts, and fails every other
// subcommand. It lets the lifecycle logic be tested without a VM.
func fakeLimactl(t *testing.T, status string, mounts ...Mount) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "limactl")

	json := `{"name":"crew-test","status":"` + status + `","config":{"mounts":[`
	for i, m := range mounts {
		if i > 0 {
			json += ","
		}
		writable := "false"
		if m.Writable {
			writable = "true"
		}
		json += `{"location":"` + m.Location + `","mountPoint":"` + m.MountPoint + `","writable":` + writable + `}`
	}
	json += `]}}`

	script := `#!/bin/sh
case "$1" in
  list)
    case "$3" in
      --json) printf '%s\n' '` + json + `' ;;
      *) printf '%s\n' "` + status + `" ;;
    esac
    ;;
  *) echo "unexpected subcommand: $1" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
