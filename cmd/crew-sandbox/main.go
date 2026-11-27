// Command crew-sandbox is the operator's handle on the guest VM. It is the only
// entry point, so the VM is only ever created from the embedded definition and
// every gate runs the same way.
//
// The contract is deliberately tool-agnostic: `crew-sandbox run --workspace DIR
// -- COMMAND` starts the guest if needed, runs COMMAND inside it, and returns its
// exit status. No orchestrator is named here; anything that runs commands adapts
// to this contract in examples/.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aditya-sathish-rao/crew-sandbox/internal/broker"
	"github.com/aditya-sathish-rao/crew-sandbox/internal/lima"
	"github.com/aditya-sathish-rao/crew-sandbox/internal/proxy"
	"github.com/aditya-sathish-rao/crew-sandbox/internal/vault"
	"github.com/aditya-sathish-rao/crew-sandbox/verify"
)

const usage = `usage: crew-sandbox <command> [flags] [-- args]

  run      start the guest if needed and run a command inside it
  attach   open an interactive session in the guest as the agent account
  up       create the guest if missing and start it
  down     stop the guest and revoke the broker's tokens
  destroy  delete the guest and its disk
  status   show Lima's view of the guest
  broker   run the token broker in the foreground
  proxy    run the egress proxy in the foreground
  check    run the verification gates

run flags:
  --workspace DIR   host directory to expose to the guest (default: none)
  --writable        expose the workspace read-write; a writable workspace is host code
                    execution (see the note after this list)
  --name NAME       Lima instance name (default: crew-sandbox)
  --workdir DIR     directory inside the guest to run in

attach flags:
  --name NAME       Lima instance name (default: crew-sandbox)
  --workdir DIR     directory inside the guest to start in (default: the agent's home)

proxy flags:
  --model-provider NAME  provider label used in logs (default: model)
  --model-upstream URL   model provider base URL; empty disables the gateway
  --model-key-env NAME   vault secret holding the model key (default: CREW_MODEL_KEY)
  --model-port PORT      model gateway listen port (default: 3130)
  --model-token-file FILE  read the model token from FILE instead of the guest
  --egress-allow FILE    hostname allowlist; empty uses the embedded example

broker flags:
  --repos-allow FILE     repository allowlist; empty uses the embedded example

check flags:
  --name NAME            Lima instance name (default: crew-sandbox)
  --workspace DIR        the host directory the guest already mounts, if any
  --writable             that mount is read-write rather than read-only
  --egress-allow FILE    hostname allowlist the proxy gate enforces
  --repos-allow FILE     repository allowlist the broker gate mints against
  --expected-tools LIST  space-separated tools the toolchain gate must find
  --model-upstream URL   model provider base URL the gateway forwards to
  --model-key-env NAME   vault secret holding the model key (default: CREW_MODEL_KEY)
  --app-id ID            GitHub App ID for the broker gate (public config)
  --other-repo OWNER/NAME  another repository the App is installed on, to prove scoping
  --broker-port PORT     port the broker gate starts (default: 3139)
  --redteam-log FILE     keep the adversarial transcript in this file
  --redteam-repos LIST   owner/name repositories the red team tries to push to (default: workspace origin or the project repo)

check phases:
  guest, root, toolchain, proxy, model, broker, redteam, all (default: guest)

The guest sees nothing of the host unless --workspace is given. That is the
boundary: a workspace mount is the one deliberate exception, and it is read-only
unless --writable is passed. Commands run as the unprivileged agent account, never
as the Lima admin account.

attach is the interactive counterpart to run. run forces --tty=false because its
output is data, so it cannot carry a full-screen program; attach allocates a pty
and lands in a login shell as the agent account, in the agent's home. Pass a
command after -- to run it instead of a shell: crew-sandbox attach -- herdr.

A workspace is refused if it is, contains, or is inside the home directory, ~/.ssh,
~/.aws, Lima's instance store, or /. Symlinks are resolved before that check, so a
link cannot stand in for one of them. --writable is a host-code-execution path
rather than a convenience: an agent that can write .git/hooks, .git/config
(core.fsmonitor, core.hooksPath), a Makefile or an .envrc gets code running on the
operator's machine the next time git or make runs there.

The model gateway has no hardcoded vendor: it forwards to whatever --model-upstream
names and authenticates with the secret --model-key-env names. It speaks the
OpenAI-compatible surface only - GET /models and POST /chat/completions, with
Authorization: Bearer, over https - so a provider that authenticates differently
needs a change here, not just a flag. With no upstream the gateway is disabled and
egress enforcement still runs.

The model token is what a client presents to the gateway. By default it is read
from /etc/crew-model-token inside the guest, which is the VM boundary's shape.
--model-token-file names a host file instead, for a fleet that runs on the host
with no guest to read from; the file must be readable by the account the proxy
runs as, and an unreadable or empty file stops the proxy rather than starting a
gateway that refuses every request.
`

// exitError carries a child process's exit status up to main.
//
// It is returned rather than exiting where the failure happens, because os.Exit
// skips deferred cleanup: the temp directories cmdCheck, cmdBroker and cmdProxy
// defer away are only removed if the command function returns normally. Exiting
// from inside them left crew-gates-* and crew-proxy-* directories behind whenever
// a gate or a service exited non-zero.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func main() {
	err := run(os.Args[1:])
	var exit exitError
	switch {
	case errors.As(err, &exit):
		// The child already wrote its own diagnostics; a "crew-sandbox:" prefix
		// here would misattribute them.
		os.Exit(exit.code)
	case err != nil:
		fmt.Fprintf(os.Stderr, "crew-sandbox: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("no command given")
	}
	command, rest := args[0], args[1:]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch command {
	case "run":
		return cmdRun(ctx, rest)
	case "attach":
		return cmdAttach(ctx, rest)
	case "up":
		return cmdUp(ctx, rest)
	case "down":
		return cmdDown(ctx, rest)
	case "destroy":
		return cmdDestroy(ctx, rest)
	case "status":
		return cmdStatus(ctx, rest)
	case "broker":
		return cmdBroker(ctx, rest)
	case "proxy":
		return cmdProxy(ctx, rest)
	case "check":
		return cmdCheck(ctx, rest)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", command)
	}
}

// guestFlags are the flags shared by every command that touches the VM.
type guestFlags struct {
	name      string
	workspace string
	writable  bool
	workdir   string
}

func (f *guestFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&f.name, "name", "crew-sandbox", "Lima instance name")
	fs.StringVar(&f.workspace, "workspace", "", "host directory to expose to the guest")
	fs.BoolVar(&f.writable, "writable", false, "expose the workspace read-write")
	fs.StringVar(&f.workdir, "workdir", "", "directory inside the guest to run in")
}

// guest builds the Lima handle from the flags. The workspace mount is the only
// per-run variation; everything else comes from the embedded definition.
func (f *guestFlags) guest() (lima.Guest, error) {
	g := lima.Guest{Name: f.name}
	if f.workspace == "" {
		return g, nil
	}
	abs, err := filepath.Abs(f.workspace)
	if err != nil {
		return g, err
	}
	// Resolve before validating. A textual check on the path as typed is defeated
	// by one symlink: `--workspace ./innocent` pointing at the home directory would
	// pass every string comparison and mount the home directory.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return g, fmt.Errorf("workspace %s: %w", f.workspace, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return g, fmt.Errorf("workspace %s: %w", resolved, err)
	}
	if !info.IsDir() {
		return g, fmt.Errorf("workspace %s is not a directory", resolved)
	}
	if err := checkWorkspace(resolved); err != nil {
		return g, err
	}
	// The mount uses the resolved path, so what the guest mounts is the directory
	// that was checked rather than a link that could be repointed afterwards. The
	// mount point mirrors it so a command's arguments mean the same on both sides.
	g.Mounts = []lima.Mount{{
		Location:   resolved,
		MountPoint: resolved,
		Writable:   f.writable,
	}}
	return g, nil
}

// forbiddenPath is a host path a workspace must never be, contain, or (for a
// credential or state subtree) sit inside.
type forbiddenPath struct {
	path string
	why  string
	// subtree refuses a workspace that is merely inside this path. It is false for
	// "/" and for the home directory: every path is inside /, and a directory inside
	// home is the ordinary case this whole flag exists to allow. It is true for the
	// credential stores, where a subdirectory is the same leak by a shorter path.
	subtree bool
}

// forbiddenWorkspaces lists what a workspace must not touch. Each entry is either a
// credential store the guest has no business reading, or state whose corruption
// hands the guest the host.
func forbiddenWorkspaces() []forbiddenPath {
	var forbidden []forbiddenPath
	add := func(path, why string, subtree bool) {
		if path == "" {
			return
		}
		forbidden = append(forbidden, forbiddenPath{path: path, why: why, subtree: subtree})
	}
	add("/", "the filesystem root", false)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return forbidden
	}
	add(home, "the home directory", false)
	add(filepath.Join(home, ".ssh"), "the SSH keys and config", true)
	add(filepath.Join(home, ".aws"), "the AWS credentials", true)
	// Lima keeps each instance's disk and definition under its store. A writable
	// mount of it would let the guest rewrite the disk it is running from.
	store := os.Getenv("LIMA_HOME")
	if store == "" {
		store = filepath.Join(home, ".lima")
	}
	add(store, "Lima's instance store", true)
	return forbidden
}

// checkWorkspace refuses a workspace that is, contains, or is inside a forbidden
// path. The caller has already resolved symlinks.
//
// The directions matter differently. A workspace that *contains* ~/.ssh lets the
// guest read the operator's keys; one that *is inside* ~/.ssh is the same leak by a
// shorter path; and one that contains Lima's store lets the guest rewrite the disk it
// runs from. Being inside the home directory is deliberately fine, because that is
// where a project normally lives.
func checkWorkspace(resolved string) error {
	for _, forbidden := range forbiddenWorkspaces() {
		target := forbidden.path
		if real, err := filepath.EvalSymlinks(target); err == nil {
			target = real
		}
		switch {
		case resolved == target:
			return fmt.Errorf("workspace %s is %s; expose a directory that does not include it", resolved, forbidden.why)
		case forbidden.subtree && within(resolved, target):
			return fmt.Errorf("workspace %s is inside %s (%s); expose a directory outside it", resolved, target, forbidden.why)
		case within(target, resolved):
			return fmt.Errorf("workspace %s contains %s (%s); expose a directory that does not include it", resolved, target, forbidden.why)
		}
	}
	return nil
}

// within reports whether child is parent or below it, comparing whole path
// components so /a/bc is not treated as being inside /a/b.
func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var f guestFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	argv := fs.Args()
	if len(argv) == 0 {
		return errors.New("run: no command given; use `crew-sandbox run --workspace DIR -- COMMAND`")
	}

	g, err := f.guest()
	if err != nil {
		return err
	}
	if err := g.Start(); err != nil {
		return err
	}

	// Untrusted work runs as the unprivileged agent account; lima.Run applies that
	// wrapper itself, so there is no way for a call site to forget it.
	err = g.RunIO(ctx, argv, f.workdir, os.Stdin, os.Stdout, os.Stderr)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitError{code: exitErr.ExitCode()}
	}
	return err
}

// cmdAttach opens an interactive session in the guest as the unprivileged agent
// account. It is the only way to reach a full-screen program in the guest: run
// forces --tty=false, so it cannot carry one.
//
// It takes --name and --workdir only. --workspace and --writable are deliberately
// absent: attach runs no command against a mount, and accepting the flags would
// suggest a mount could be added to an instance that already exists.
func cmdAttach(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	var f guestFlags
	fs.StringVar(&f.name, "name", "crew-sandbox", "Lima instance name")
	fs.StringVar(&f.workdir, "workdir", "", "directory inside the guest to start in")
	if err := fs.Parse(args); err != nil {
		return err
	}
	argv := fs.Args()

	g := lima.Guest{Name: f.name}
	// StartForAttach, not Start: attach runs nothing against a mount, so an instance
	// created with a workspace must still be attachable.
	if err := g.StartForAttach(); err != nil {
		return err
	}

	err := g.Attach(ctx, argv, f.workdir, os.Stdin, os.Stdout, os.Stderr)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitError{code: exitErr.ExitCode()}
	}
	return err
}

func cmdUp(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	var f guestFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	g, err := f.guest()
	if err != nil {
		return err
	}
	if err := g.Start(); err != nil {
		return err
	}
	fmt.Printf("crew-sandbox: %s is running\n", g.Name)
	return nil
}

func cmdDown(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	var f guestFlags
	f.bind(fs)
	brokerPort := fs.Int("broker-port", 3129, "port of the running token broker")
	if err := fs.Parse(args); err != nil {
		return err
	}
	g, err := f.guest()
	if err != nil {
		return err
	}

	// Stop before revoking: a token minted while the guest is still up would
	// otherwise survive the sweep. Revoke even if the stop fails, because a
	// credential that outlives the VM is worse than a VM that did not stop.
	// Each half decides the exit status; neither can skip the other.
	stopErr := g.Stop()
	if stopErr != nil {
		fmt.Fprintf(os.Stderr, "crew-sandbox: the guest did not stop; revoking anyway: %v\n", stopErr)
	} else {
		fmt.Printf("crew-sandbox: %s is stopped\n", g.Name)
	}

	// The revoke must not be cancelled by a Ctrl-C that interrupted the stop.
	revokeCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	revoked, revokeErr := broker.RevokeAll(revokeCtx, fmt.Sprintf("http://127.0.0.1:%d", *brokerPort))
	switch {
	case errors.Is(revokeErr, syscall.ECONNREFUSED):
		fmt.Fprintln(os.Stderr, "crew-sandbox: no token broker is running; no tokens to revoke")
		revokeErr = nil
	case revokeErr != nil:
		fmt.Fprintf(os.Stderr, "crew-sandbox: the broker did not revoke its tokens: %v\n", revokeErr)
	default:
		fmt.Printf("crew-sandbox: revoked %d of %d broker tokens\n", revoked.Revoked, revoked.Tasks)
	}
	return errors.Join(stopErr, revokeErr)
}

func cmdDestroy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("destroy", flag.ContinueOnError)
	var f guestFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	g, err := f.guest()
	if err != nil {
		return err
	}
	if err := g.Destroy(); err != nil {
		return err
	}
	fmt.Printf("crew-sandbox: %s is destroyed\n", g.Name)
	return nil
}

func cmdStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	var f guestFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	g, err := f.guest()
	if err != nil {
		return err
	}
	status, err := g.Status()
	if err != nil {
		return err
	}
	if status == lima.StatusUnknown {
		fmt.Printf("crew-sandbox: %s does not exist\n", g.Name)
		return nil
	}
	fmt.Printf("crew-sandbox: %s is %s\n", g.Name, status)
	return nil
}

// cmdBroker runs the token broker in the foreground. The private key comes from
// the vault provider, never from argv or a file the operator has to manage.
func cmdBroker(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("broker", flag.ContinueOnError)
	appID := fs.String("app-id", "", "GitHub App ID (not a secret)")
	port := fs.Int("port", 0, "listen port (default 3129)")
	installation := fs.String("installation", "", "pin the installation ID")
	reposAllow := fs.String("repos-allow", "", "repository allowlist file; empty uses the embedded example")
	selftest := fs.Bool("selftest", false, "run the signing self-test and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	key, err := secret(ctx, "GITHUB_APP_PRIVATE_KEY")
	if err != nil {
		return err
	}
	cfg := broker.Config{
		AppID:          *appID,
		PrivateKey:     key,
		Port:           *port,
		InstallationID: *installation,
	}
	if *reposAllow != "" {
		repos, err := os.ReadFile(*reposAllow)
		if err != nil {
			return fmt.Errorf("repos allowlist: %w", err)
		}
		cfg.Repos = repos
	}
	if *selftest {
		return broker.SelfTest(ctx, cfg)
	}
	p, err := broker.Command(ctx, cfg)
	if err != nil {
		return err
	}
	defer p.Cleanup()
	return runForeground(p.Cmd)
}

// cmdProxy runs the egress proxy in the foreground. The model key comes from the
// vault provider; the guest's model token is read from the guest, so neither is
// baked into the binary. The provider is a setting: the operator names the
// upstream and the secret that authenticates it, so the gateway serves any
// provider rather than one hardcoded vendor.
func cmdProxy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	var f guestFlags
	f.bind(fs)
	port := fs.Int("port", 0, "listen port (default 3128)")
	selftest := fs.Bool("selftest", false, "run the ClientHello parser self-test and exit")
	providerName := fs.String("model-provider", "model", "provider label used in logs")
	upstream := fs.String("model-upstream", "", "model provider base URL; empty disables the gateway")
	keyEnv := fs.String("model-key-env", "CREW_MODEL_KEY", "vault secret name holding the model key")
	keyHashOut := fs.String("key-hash-out", "", "write the model key's SHA-256 to this file, for the model gate")
	modelPort := fs.Int("model-port", 0, "model gateway listen port (default 3130)")
	egressAllow := fs.String("egress-allow", "", "hostname allowlist file; empty uses the embedded example")
	modelTokenFile := fs.String("model-token-file", "", "read the model token from this file instead of the guest")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// The self-test exercises the ClientHello parser and needs no secret, so it
	// runs before the vault is touched. Asking for a key first would make a
	// secret-free check depend on an interactive vault prompt.
	if *selftest {
		return proxy.SelfTest(ctx, proxy.Config{Port: *port})
	}

	cfg := proxy.Config{
		Provider: proxy.Provider{
			Name:     *providerName,
			Upstream: *upstream,
		},
		Port:        *port,
		ModelPort:   *modelPort,
		KeyHashPath: *keyHashOut,
	}
	if *egressAllow != "" {
		allow, err := os.ReadFile(*egressAllow)
		if err != nil {
			return fmt.Errorf("egress allowlist: %w", err)
		}
		cfg.Allow = allow
	}

	// The model key is optional. Egress enforcement does not need it, and a proxy
	// that refuses to start without a model key cannot enforce egress on a host
	// where the model is not in use. A missing key disables the model gateway and
	// leaves the allowlist in force. With no upstream configured there is nothing
	// to authenticate, so the vault is not consulted at all.
	if cfg.Provider.Upstream != "" {
		key, err := secret(ctx, *keyEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "crew-sandbox: model gateway disabled: %v\n", err)
		}
		cfg.ModelKey = key
	}

	// The model token is what the client presents to the gateway; the model key is
	// what the gateway presents upstream. They are independent, and the token is
	// never the key.
	//
	// There can be two clients on one gateway. A machine that runs the fleet in a
	// guest and also as a dedicated macOS account has a token in each place, and
	// both must be accepted. The guest's token is read from the guest; the host
	// account's comes from a file the operator owns, named by --model-token-file.
	// Either source alone is a complete configuration.
	if *modelTokenFile != "" {
		token, err := os.ReadFile(*modelTokenFile)
		if err != nil {
			return fmt.Errorf("model token file: %w", err)
		}
		cfg.ModelToken = strings.TrimSpace(string(token))
		if cfg.ModelToken == "" {
			return fmt.Errorf("model token file %s is empty", *modelTokenFile)
		}
	}

	// The guest's token lives in the guest. Reading it here keeps it out of the
	// host's environment and out of the binary's configuration. A guest that is not
	// running is not an error: a host-only fleet has no guest at all, and a machine
	// that runs the fleet as a dedicated macOS account may never have created one.
	//
	// A guest that is running but has no token yet is also not an error: the token
	// is created inside the VM, so a proxy started before the guest's first boot
	// would otherwise refuse to start and never come back.
	if g, err := f.guest(); err == nil {
		// The token is 0644 by design, so the agent account can read it without
		// sudo; Run already drops to agent, and a nested `sudo -n` would fail there.
		if token, err := g.Run([]string{"cat", "/etc/crew-model-token"}, ""); err == nil {
			token = strings.TrimSpace(token)
			if token != "" && token != cfg.ModelToken {
				cfg.ModelTokens = append(cfg.ModelTokens, token)
			}
		}
	}

	p, err := proxy.Command(ctx, cfg)
	if err != nil {
		return err
	}
	defer p.Cleanup()
	return runForeground(p.Cmd)
}

// cmdCheck runs the verification gates. The gates are bash, because they drive
// limactl and assert on a guest as it is right now; this command is the seam that
// puts them where bash can reach them and names the binary that starts the host
// services they need.
//
// The gate set is embedded, so the gates that run are the gates this binary was
// built with. A phase that cannot be run — no upstream configured, no App ID — says
// so and fails, rather than reporting a pass it did not earn.
func cmdCheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	name := fs.String("name", "crew-sandbox", "Lima instance name")
	workspace := fs.String("workspace", envOr("CREW_WORKSPACE", ""), "host directory the guest mounts, if any")
	writable := fs.Bool("writable", os.Getenv("CREW_WRITABLE") == "true", "the workspace mount is read-write")
	egressAllow := fs.String("egress-allow", envOr("CREW_EGRESS_ALLOW", ""), "hostname allowlist the proxy gate enforces")
	reposAllow := fs.String("repos-allow", envOr("CREW_REPOS_ALLOW", ""), "repository allowlist the broker gate mints against")
	expectedTools := fs.String("expected-tools", envOr("CREW_EXPECTED_TOOLS", ""), "space-separated tools the toolchain gate must find")
	modelUpstream := fs.String("model-upstream", envOr("CREW_MODEL_UPSTREAM", ""), "model provider base URL the gateway forwards to")
	modelKeyEnv := fs.String("model-key-env", envOr("CREW_MODEL_KEY_ENV", "CREW_MODEL_KEY"), "vault secret holding the model key")
	appID := fs.String("app-id", envOr("CREW_APP_ID", ""), "GitHub App ID for the broker gate")
	otherRepo := fs.String("other-repo", envOr("CREW_BROKER_OTHER_REPO", ""), "another repository the App is installed on but the token is not scoped to")
	brokerPortDefault, _ := strconv.Atoi(os.Getenv("CREW_BROKER_PORT"))
	if brokerPortDefault == 0 {
		brokerPortDefault = 3139
	}
	brokerPort := fs.Int("broker-port", brokerPortDefault, "port the broker gate starts")
	redteamLog := fs.String("redteam-log", envOr("CREW_REDTEAM_LOG", ""), "keep the adversarial transcript in this file")
	redteamRepos := fs.String("redteam-repos", envOr("CREW_REDTEAM_REPOS", ""), "space-separated owner/name repositories the red team tries to push to; empty uses the workspace origin or the project repo")
	// The phase list is the top-level usage, so -h shows that rather than flag's
	// own dump of the flags.
	fs.Usage = func() {}
	// The phase may come before or after the flags: "check guest --workspace DIR"
	// reads the way an operator types it, and flag.Parse stops at the first
	// positional argument.
	phase := "guest"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		phase = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usage)
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("check: unexpected argument %q", fs.Arg(0))
	}

	dir, err := os.MkdirTemp("", "crew-gates-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	driver, err := verify.Stage(dir)
	if err != nil {
		return err
	}

	// The gates start the proxy and the broker through this binary, so they need
	// the path that was invoked. Under the packaged wrapper that path is the
	// wrapper, which is what puts limactl and node on PATH for the child;
	// os.Executable would name the unwrapped binary and lose them.
	self, err := exec.LookPath(os.Args[0])
	if err != nil {
		return fmt.Errorf("check: cannot locate the crew-sandbox binary: %w", err)
	}

	cmd := exec.CommandContext(ctx, "bash", driver, phase)
	cmd.Env = append(os.Environ(),
		"CREW_SELF="+self,
		"CREW_NAME="+*name,
		"CREW_WORKSPACE="+*workspace,
		"CREW_WRITABLE="+strconv.FormatBool(*writable),
		"CREW_EGRESS_ALLOW="+*egressAllow,
		"CREW_REPOS_ALLOW="+*reposAllow,
		"CREW_EXPECTED_TOOLS="+*expectedTools,
		"CREW_MODEL_UPSTREAM="+*modelUpstream,
		"CREW_MODEL_KEY_ENV="+*modelKeyEnv,
		"CREW_APP_ID="+*appID,
		"CREW_BROKER_OTHER_REPO="+*otherRepo,
		"CREW_BROKER_PORT="+strconv.Itoa(*brokerPort),
		"CREW_REDTEAM_LOG="+*redteamLog,
		"CREW_REDTEAM_REPOS="+*redteamRepos,
	)
	return runForeground(cmd)
}

// envOr is the default for a flag whose operator already set the environment.
// The gates take policy as environment variables, so exporting one has to mean the
// same as passing the flag.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// secret resolves a name through the vault chain. The environment is checked
// first: an operator who exported the secret has already made the decision, and
// asking the vault would block on an approval prompt for a value we already have.
// Atomic Vault is the fallback and the default for anyone who has not exported it.
func secret(ctx context.Context, name string) (string, error) {
	// The operator approves a vault prompt by hand, which can take longer than the
	// provider's non-interactive default. The chain still falls through on
	// "not available", so a missing secret fails loudly rather than hanging.
	provider := vault.Chain(vault.FromEnv{}, vault.AtomicVault{Timeout: 60 * time.Second})
	value, err := provider.Get(ctx, name)
	if err != nil {
		return "", fmt.Errorf("%s: %w (export it or set it in Atomic Vault)", name, err)
	}
	return value, nil
}

// runForeground runs a child process to completion with the operator's own streams,
// waits for it, and returns its exit status so the caller's deferred cleanup still
// runs.
//
// Signals reach the child through the context it was built with: run() installs a
// signal-aware context, so Ctrl-C cancels it and exec cancels the child. The
// cancellation is set to SIGTERM with a grace period rather than exec's default
// SIGKILL, because the broker and the proxy both have something to release when
// asked politely - the broker's tokens live only in its memory.
func runForeground(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	// Unconditionally, not "if unset": exec.CommandContext installs its own Cancel
	// that calls Process.Kill, so a nil check would keep the SIGKILL. A process that
	// has already exited reports ErrProcessDone, which is not a cancellation failure.
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 10 * time.Second
	}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitError{code: exitErr.ExitCode()}
		}
		return err
	}
	return nil
}
