package verify

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestScriptsParse covers the failure that would otherwise surface at the worst
// moment: the scripts ship as data, so a syntax error in one is not noticed until
// an operator runs that phase against a live guest.
func TestScriptsParse(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not on PATH")
	}
	dir := t.TempDir()
	err := fs.WalkDir(scripts, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(name) != ".sh" {
			return err
		}
		data, err := scripts.ReadFile(name)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, filepath.Base(name))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse: %v\n%s", name, err, out)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded scripts: %v", err)
	}
}

// TestStageShipsEveryRunner guards the embedding pattern: a file missing from it
// builds and runs, and only fails when a phase tries to stage a script that is not
// in the binary.
func TestStageShipsEveryRunner(t *testing.T) {
	dir := t.TempDir()
	driver, err := Stage(dir)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	for _, path := range []string{
		driver,
		filepath.Join(dir, "redteam", "redteam.sh"),
		filepath.Join(dir, "gates", "broker-ruleset.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("not staged: %v", err)
		}
	}
}
