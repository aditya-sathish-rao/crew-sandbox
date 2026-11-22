// Package verify carries the acceptance criteria: the phase gates in gates/ and the
// adversarial script in redteam/.
//
// They are embedded rather than read from the source tree because the operator's
// binary is the only thing that ships. A script left beside its source would be
// invisible to anyone who installed the flake, and `crew-sandbox check` would have
// nothing to run.
package verify

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed gates/*.sh gates/*.json redteam/*.sh
var scripts embed.FS

// Stage writes the scripts under dir, keeping gates/ and redteam/ apart so the gate
// driver can reach the adversarial script as its sibling, and returns the path of
// the driver.
//
// Only the owner can read or execute the copies: from the moment they are staged
// until root runs one of them, nothing in the guest may be able to rewrite them.
func Stage(dir string) (driver string, err error) {
	err = fs.WalkDir(scripts, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := scripts.ReadFile(name)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if filepath.Ext(name) == ".sh" {
			mode = 0o700
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, mode)
	})
	if err != nil {
		return "", fmt.Errorf("verify: %w", err)
	}
	return filepath.Join(dir, "gates", "check.sh"), nil
}
