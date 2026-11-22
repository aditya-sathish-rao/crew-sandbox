// Package lima drives the guest VM through limactl. It is the only package that
// knows the VM exists; everything above it works in terms of "run this command
// in the guest".
//
// The VM definition is embedded rather than read from disk so a built binary
// cannot be pointed at a different definition by editing a file next to it. The
// one thing that varies per run is the workspace mount, which is applied as a
// limactl --set override so the embedded definition stays the single source of
// truth for everything else.
package lima

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed lima.yaml
var definition []byte

// ErrNotInstalled is returned when limactl is not on PATH. The caller turns this
// into an actionable message rather than a stack trace.
var ErrNotInstalled = errors.New("limactl not found on PATH")

// Guest is a handle on one Lima instance.
type Guest struct {
	// Name is the Lima instance name. It is the unit of lifecycle: one name, one
	// VM, one disk.
	Name string
	// Definition is the YAML the VM is created from. Empty means the embedded
	// definition.
	Definition []byte
	// Mounts are host directories to expose to the guest. Empty means the guest
	// sees nothing of the host, which is the default the embedded definition
	// already encodes.
	Mounts []Mount
	// Limactl is the binary to invoke. Empty means "limactl" from PATH.
	Limactl string
}

// Mount is one host directory exposed to the guest. Writable is false unless the
// caller asks for it; a read-only mount is the safe default for a workspace the
// guest only needs to read.
type Mount struct {
	Location   string
	MountPoint string
	Writable   bool
}

// Status is Lima's view of an instance.
type Status string

const (
	StatusRunning Status = "Running"
	StatusStopped Status = "Stopped"
	StatusUnknown Status = ""
)

func (g Guest) binary() string {
	if g.Limactl != "" {
		return g.Limactl
	}
	return "limactl"
}

func (g Guest) definition() []byte {
	if len(g.Definition) > 0 {
		return g.Definition
	}
	return definition
}

// Status reports whether the instance is running. A missing instance is reported
// as StatusUnknown with no error: "not created yet" is a normal state, not a
// failure.
func (g Guest) Status() (Status, error) {
	out, err := g.run("list", g.Name, "--format", "{{.Status}}")
	if err != nil {
		if isNotInstalled(err) {
			return StatusUnknown, ErrNotInstalled
		}
		// limactl list exits non-zero when the instance does not exist.
		return StatusUnknown, nil
	}
	return Status(strings.TrimSpace(out)), nil
}

// ErrMountsNotApplied is returned when a mount was requested for an instance that
// already exists. Lima fixes mounts at creation time, so starting an existing
// instance cannot add one. Silently ignoring the request would run the command
// without the workspace the operator asked for, which is worse than refusing.
var ErrMountsNotApplied = errors.New("mounts cannot be added to an existing instance")

// Start creates the instance if it is missing and starts it. It is idempotent:
// starting a running instance is a no-op.
//
// A mount is only honoured when the instance is created. If mounts were requested
// and the existing instance does not already have them, Start refuses rather than
// running the command without the workspace. An instance that already carries the
// requested mount is fine: the second and later runs of the same workspace must
// work, not fail.
func (g Guest) Start() error {
	status, err := g.Status()
	if err != nil {
		return err
	}
	if status == StatusUnknown {
		return g.create()
	}

	applied, err := g.appliedMounts()
	if err != nil {
		return err
	}
	if !mountsSatisfied(g.Mounts, applied) {
		return fmt.Errorf("%w: %s already exists without them; destroy it first", ErrMountsNotApplied, g.Name)
	}
	if status == StatusRunning {
		return nil
	}
	_, err = g.run("start", "--tty=false", g.Name)
	return err
}

// appliedMounts reads the mounts an existing instance was created with. Lima
// records them in the instance's own lima.yaml, which is the authoritative copy:
// the definition the operator passed is not what the instance is running.
func (g Guest) appliedMounts() ([]Mount, error) {
	out, err := g.run("list", g.Name, "--json")
	if err != nil {
		return nil, err
	}
	var info struct {
		Config struct {
			Mounts []struct {
				Location   string `json:"location"`
				MountPoint string `json:"mountPoint"`
				Writable   bool   `json:"writable"`
			} `json:"mounts"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return nil, fmt.Errorf("limactl list %s --json: %w", g.Name, err)
	}
	mounts := make([]Mount, 0, len(info.Config.Mounts))
	for _, m := range info.Config.Mounts {
		mounts = append(mounts, Mount{
			Location:   m.Location,
			MountPoint: m.MountPoint,
			Writable:   m.Writable,
		})
	}
	return mounts, nil
}

// mountsSatisfied reports whether every requested mount is already present with
// the same location, mount point and writability. A read-only request is not
// satisfied by a writable mount: the guest would have more access than asked for.
func mountsSatisfied(want, have []Mount) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h.Location == w.Location && h.MountPoint == w.MountPoint && h.Writable == w.Writable {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// create writes the definition to a temporary file and creates the instance from
// it. The mount overrides are applied with --set so the embedded definition keeps
// its "mount nothing" default and the mount is visibly a per-run decision.
//
// `limactl create` only creates; it leaves the instance stopped. Start is called
// afterwards so a first run actually runs the command instead of failing with
// "instance is stopped".
func (g Guest) create() error {
	dir, err := os.MkdirTemp("", "crew-sandbox-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "lima.yaml")
	if err := os.WriteFile(path, g.definition(), 0o600); err != nil {
		return err
	}

	args := []string{"create", "--tty=false", "--name", g.Name}
	for _, m := range g.Mounts {
		args = append(args, "--set", mountOverride(m))
	}
	args = append(args, path)

	if _, err = g.run(args...); err != nil {
		return err
	}
	_, err = g.run("start", "--tty=false", g.Name)
	return err
}

// mountOverride renders one mount as a yq expression for limactl --set. The
// expression appends to the existing list so the definition's own mounts are
// preserved rather than replaced.
func mountOverride(m Mount) string {
	loc := strings.ReplaceAll(m.Location, `"`, `\"`)
	point := strings.ReplaceAll(m.MountPoint, `"`, `\"`)
	writable := "false"
	if m.Writable {
		writable = "true"
	}
	return fmt.Sprintf(
		`.mounts += [{"location": "%s", "mountPoint": "%s", "writable": %s}]`,
		loc, point, writable,
	)
}

// Stop stops the instance. Stopping a stopped instance is a no-op.
func (g Guest) Stop() error {
	status, err := g.Status()
	if err != nil {
		return err
	}
	if status != StatusRunning {
		return nil
	}
	_, err = g.run("stop", g.Name)
	return err
}

// Destroy deletes the instance and its disk. It is the only operation that
// discards guest state, so it is never called implicitly.
func (g Guest) Destroy() error {
	_, err := g.run("delete", "--force", g.Name)
	return err
}

// Run executes a command in the guest and returns its combined output. The
// command is passed as argv, never through a shell, so a workspace path or an
// argument containing spaces or quotes cannot become a second command.
func (g Guest) Run(argv []string, workdir string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no command given")
	}
	args := []string{"shell", "--tty=false", g.Name}
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	args = append(args, "--")
	args = append(args, argv...)
	return g.run(args...)
}

// run invokes limactl and returns stdout. stderr is folded into the error so a
// failure carries Lima's own explanation.
func (g Guest) run(args ...string) (string, error) {
	cmd := exec.Command(g.binary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("limactl %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

func isNotInstalled(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr)
}
