package broker

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// broker.mjs ships as data, so a syntax error would otherwise surface only when
// the broker starts with a real key. `node --check` catches it in the same run as
// the gates' `bash -n`; `nix flake check` executes this.
func TestScriptParses(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}
	path := filepath.Join(t.TempDir(), "broker.mjs")
	if err := os.WriteFile(path, script, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("node", "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("broker.mjs does not parse: %v\n%s", err, out)
	}
}
