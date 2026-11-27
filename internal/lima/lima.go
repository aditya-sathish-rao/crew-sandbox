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
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

//go:embed lima.yaml
var definition []byte

// ErrNotInstalled is returned when limactl is not on PATH. The caller turns this
// into an actionable message rather than a stack trace.
var ErrNotInstalled = errors.New("limactl not found on PATH")

// AgentUser is the unprivileged account inside the guest. The Lima user is the
// administration account and keeps passwordless sudo, so anything the operator
// did not write must run as AgentUser rather than as the Lima user.
const AgentUser = "agent"

// agentWrap is the sudo invocation that drops from the Lima user to AgentUser.
const agentWrap = "sudo"

// asAgent wraps argv so the guest runs it as AgentUser instead of as the Lima user.
//
// It is unexported on purpose: Run and RunIO wrap unconditionally, so there is no
// way for a caller to run a guest command as the administration account by
// forgetting a wrapper. That is the difference between a boundary and a convention.
//
// `sudo -n` fails closed: if the guest ever stopped permitting it, the command
// fails rather than silently running as the administration account. `-H` points
// HOME at the agent's home, so tool configuration and caches are the agent's
// rather than the admin account's.
func asAgent(argv []string) []string {
	wrapped := make([]string, 0, len(argv)+6)
	wrapped = append(wrapped, agentWrap, "-n", "-u", AgentUser, "-H", "--")
	return append(wrapped, argv...)
}

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
	// SSH is the ssh client Attach invokes. Empty means "ssh" from PATH.
	SSH string
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
	return g.start(true)
}

// StartForAttach starts the instance if it is stopped, without the mount check
// Start applies.
//
// Attach runs no command against a mount: it is the operator's own interactive
// session, not untrusted work. An instance created with a workspace must still be
// attachable, and refusing to start it because this run asked for no mount would
// make the one way into the VM depend on how the VM happened to be created.
func (g Guest) StartForAttach() error {
	return g.start(false)
}

func (g Guest) start(validateMounts bool) error {
	status, err := g.Status()
	if err != nil {
		return err
	}
	if status == StatusUnknown {
		return g.create()
	}

	if validateMounts {
		applied, err := g.appliedMounts()
		if err != nil {
			return err
		}
		if !mountsSatisfied(g.Mounts, applied) {
			return fmt.Errorf(
				"%w: %s was created with [%s] and this run asked for [%s]; destroy it and start it again",
				ErrMountsNotApplied, g.Name, describeMounts(applied), describeMounts(g.Mounts),
			)
		}
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

// describeMounts renders a mount list for an error message, so an operator can see
// which side differs: a mount they asked for and did not get, or one they did not ask
// for and already have.
func describeMounts(mounts []Mount) string {
	if len(mounts) == 0 {
		return "no mounts"
	}
	parts := make([]string, 0, len(mounts))
	for _, m := range mounts {
		mode := "ro"
		if m.Writable {
			mode = "rw"
		}
		parts = append(parts, fmt.Sprintf("%s -> %s (%s)", m.Location, m.MountPoint, mode))
	}
	return strings.Join(parts, ", ")
}

// mountsSatisfied reports whether the requested mounts and the instance's applied
// mounts are the same set.
//
// Both directions matter. A missing mount would run the command without the
// workspace the operator asked for. An *extra* mount is the more dangerous
// direction: an instance created earlier with a writable workspace carries that
// mount for its whole life, so a later run that asks for no workspace at all would
// otherwise pass this check and run against a writable host mount the operator
// believes is not there. Equality is also what makes the boundary stated in the
// README - "nothing from the host is mounted unless --workspace is given" - true of
// an existing instance rather than only of a freshly created one.
//
// A read-only request is not satisfied by a writable mount: the guest would have
// more access than asked for.
func mountsSatisfied(want, have []Mount) bool {
	if len(want) != len(have) {
		return false
	}
	matched := make([]bool, len(have))
	for _, w := range want {
		found := false
		for i, h := range have {
			if matched[i] {
				continue
			}
			if h.Location == w.Location && h.MountPoint == w.MountPoint && h.Writable == w.Writable {
				matched[i] = true
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

// yqString escapes a value for the double-quoted YAML scalar mountOverride builds.
// The backslash has to be escaped before the quote, or a path ending in a backslash
// would escape the closing quote and break out of the scalar.
func yqString(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}

// mountOverride renders one mount as a yq expression for limactl --set. The
// expression appends to the existing list so the definition's own mounts are
// preserved rather than replaced.
func mountOverride(m Mount) string {
	loc := yqString(m.Location)
	point := yqString(m.MountPoint)
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

// shellArgs is the limactl argv that runs argv in the guest. Run and RunIO both go
// through it so the two paths cannot drift apart.
func (g Guest) shellArgs(argv []string, workdir string) []string {
	args := []string{"shell", "--tty=false", g.Name}
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	args = append(args, "--")
	return append(args, argv...)
}

// Run executes a command in the guest as the unprivileged `agent` account and
// returns its stdout. The command is passed as argv, never through a shell, so a
// workspace path or an argument containing spaces or quotes cannot become a second
// command.
//
// The agent wrapper is part of this call, not the caller's job: the Lima user is the
// administration account with passwordless sudo, so a guest command that ran as the
// Lima user would be one `sudo` from root.
//
// Use Run when the output is data the caller inspects; use RunIO when it is output
// a human or an orchestrator is watching.
func (g Guest) Run(argv []string, workdir string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no command given")
	}
	return g.run(g.shellArgs(asAgent(argv), workdir)...)
}

// RunIO runs argv in the guest as the unprivileged `agent` account, with the
// caller's streams wired straight through. Output is forwarded as it is produced
// rather than buffered until the command exits, stdin reaches the guest command, and
// stderr keeps its own stream instead of being folded into the error.
//
// The context cancels the guest command. Cancellation sends SIGTERM and only kills
// after a grace period: CommandContext's default is an immediate SIGKILL, which
// would deny a guest process the same chance to clean up that Ctrl-C in a terminal
// gives it.
func (g Guest) RunIO(ctx context.Context, argv []string, workdir string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(argv) == 0 {
		return errors.New("no command given")
	}
	cmd := exec.CommandContext(ctx, g.binary(), g.shellArgs(asAgent(argv), workdir)...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Cancel = func() error {
		// A guest command that has already exited reports ErrProcessDone; that is not
		// a cancellation failure, and propagating it would replace the exit status.
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		// No captured stderr: it went to the caller's stream. Wrap rather than
		// format so errors.As can still recover the guest command's exit status.
		return fmt.Errorf("limactl shell: %w", err)
	}
	return nil
}

// sshHost is the Host alias Lima writes into the instance's ssh.config. Lima
// derives it from the instance name by prefixing "lima-" and replacing dots and
// underscores with dashes, so the alias is not simply "lima-" + Name.
func (g Guest) sshHost() string {
	name := strings.ReplaceAll(g.Name, ".", "-")
	name = strings.ReplaceAll(name, "_", "-")
	return "lima-" + name
}

// sshConfigPath is the ssh config Lima writes for the instance. Lima documents it
// as a file that can be passed to `ssh -F`, which is what Attach does: it carries
// the identity file, the host key policy and the forwarded port, so none of that
// has to be re-derived here and none of it can drift from what Lima started.
func (g Guest) sshConfigPath() (string, error) {
	store := os.Getenv("LIMA_HOME")
	if store == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		store = filepath.Join(home, ".lima")
	}
	return filepath.Join(store, g.Name, "ssh.config"), nil
}

// shellQuote renders s as a single word for a POSIX shell. The remote command is
// one string that the guest's login shell parses, so every argument in it has to
// survive that parse unchanged.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:=@+,", r):
		default:
			safe = false
		}
		if !safe {
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin quotes each word and joins them, producing one string a shell parses
// back into the same argv.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

// attachScript is the command the guest runs once ssh has a pty: a login shell as
// AgentUser, in AgentUser's home, running argv if one was given.
//
// The `cd` is not decoration. The ssh session starts in the Lima user's home, which
// AgentUser cannot read, so a shell that inherited that directory would start in a
// place it has no permission to be.
//
// The command goes through `bash -lc` rather than being exec'd directly, because the
// guest's tools come from its own login profile: PATH, the proxy variables and the
// Nix profile are all set there. `attach -- herdr` has to resolve herdr the same way
// the operator would if they had typed it in the guest, and a direct exec would not.
func attachScript(argv []string, workdir string) string {
	dir := `"$HOME"`
	if workdir != "" {
		dir = shellQuote(workdir)
	}
	script := "cd " + dir + " && exec bash -l"
	if len(argv) > 0 {
		script = "cd " + dir + " && exec bash -lc " + shellQuote(shellJoin(argv))
	}
	return script
}

// Attach opens an interactive session in the guest as the unprivileged AgentUser.
//
// This is the only way to reach the guest's own terminal: Run and RunIO force
// --tty=false because they are for commands whose output is data, so neither can
// carry a full-screen program. Attach goes to ssh directly, with -tt to allocate a
// pty, and lands in a login shell as AgentUser.
//
// The ssh login itself is the Lima user, because that is the account Lima installs
// the instance key for; the session becomes AgentUser through the same sudo wrapper
// Run and RunIO use. Attaching as the administration account would put the operator
// one `sudo` from root for no benefit, and would make the boundary depend on which
// command the operator happened to run.
//
// argv is optional. With none, the operator gets a shell; with some, the shell runs
// them, which is how `attach -- herdr` reaches a full-screen program in the guest.
// workdir is where the session starts; empty means AgentUser's home.
func (g Guest) Attach(ctx context.Context, argv []string, workdir string, stdin io.Reader, stdout, stderr io.Writer) error {
	config, err := g.sshConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(config); err != nil {
		return fmt.Errorf("attach: %s: %w; start the guest first", config, err)
	}

	remote := shellJoin(asAgent([]string{"bash", "-c", attachScript(argv, workdir)}))
	args := []string{
		"-F", config,
		// -tt, not -t: -t only asks for a pty when the local side has one, and the
		// point of attach is that the guest always gets one.
		"-tt",
		// Lima's own choice, for the same reason: without it ssh prints "Shared
		// connection to ... closed" over the session on exit.
		"-o", "LogLevel=ERROR",
		"--", g.sshHost(),
		remote,
	}

	ssh := g.SSH
	if ssh == "" {
		ssh = "ssh"
	}
	cmd := exec.CommandContext(ctx, ssh, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Cancel = func() error {
		// Same grace as RunIO: an interactive session gets a chance to exit cleanly
		// rather than being killed the instant the context is cancelled.
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh: %w", err)
	}
	return nil
}

// run invokes limactl and returns stdout. stderr is folded into the error so a
// failure carries Lima's own explanation.
func (g Guest) run(args ...string) (string, error) {
	cmd := exec.Command(g.binary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// %w keeps the *exec.ExitError reachable: the caller returns the guest
		// command's own status instead of collapsing every failure to 1.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.String(), fmt.Errorf("limactl %s: %w", args[0], err)
		}
		return stdout.String(), fmt.Errorf("limactl %s: %s: %w", args[0], msg, err)
	}
	return stdout.String(), nil
}

func isNotInstalled(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr)
}
