package lima

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeLimactl writes a shell script that answers `list --format {{.Status}}` with
// the given status and fails every other subcommand. It lets the lifecycle logic
// be tested without a VM.
func fakeLimactl(t *testing.T, status string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "limactl")
	script := `#!/bin/sh
case "$1" in
  list) printf '%s\n' "` + status + `" ;;
  *) echo "unexpected subcommand: $1" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
