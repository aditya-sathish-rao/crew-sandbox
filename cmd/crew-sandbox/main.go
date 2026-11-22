// Command crew-sandbox is the operator's handle on the guest VM. It is the only
// entry point, so the VM is only ever created from the embedded definition and
// every gate runs the same way.
//
// The contract is deliberately tool-agnostic: `crew-sandbox run --workspace DIR
// -- COMMAND` starts the guest if needed, runs COMMAND inside it, and returns its
// exit status. Nothing about herdr, first mate, or any other orchestrator appears
// here; those adapt to this contract in examples/.
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
	"syscall"

	"github.com/aditya-sathish-rao/crew-sandbox/internal/broker"
	"github.com/aditya-sathish-rao/crew-sandbox/internal/lima"
	"github.com/aditya-sathish-rao/crew-sandbox/internal/proxy"
	"github.com/aditya-sathish-rao/crew-sandbox/internal/vault"
)

const usage = `usage: crew-sandbox <command> [flags] [-- args]

  run      start the guest if needed and run a command inside it
  up       create the guest if missing and start it
  down     stop the guest
  destroy  delete the guest and its disk
  status   show Lima's view of the guest
  broker   run the token broker in the foreground
  proxy    run the egress proxy in the foreground
  check    run the verification gates

run flags:
  --workspace DIR   host directory to expose to the guest (default: none)
  --writable        expose the workspace read-write (default: read-only)
  --name NAME       Lima instance name (default: crew-sandbox)
  --workdir DIR     directory inside the guest to run in

proxy flags:
  --model-provider NAME  provider label used in logs (default: model)
  --model-upstream URL   model provider base URL; empty disables the gateway
  --model-key-env NAME   vault secret holding the model key (default: CREW_MODEL_KEY)

The guest sees nothing of the host unless --workspace is given. That is the
boundary: a workspace mount is the one deliberate exception, and it is read-only
unless --writable is passed.

The model gateway is provider-agnostic. It forwards to whatever --model-upstream
names and authenticates with the secret --model-key-env names, so no vendor is
hardcoded. With no upstream the gateway is disabled and egress enforcement still
runs.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
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
	info, err := os.Stat(abs)
	if err != nil {
		return g, fmt.Errorf("workspace %s: %w", abs, err)
	}
	if !info.IsDir() {
		return g, fmt.Errorf("workspace %s is not a directory", abs)
	}
	// The mount point mirrors the host path so a command's arguments mean the
	// same thing inside and outside the guest. That is a convenience, not a
	// security property: the guest still only sees this one directory.
	g.Mounts = []lima.Mount{{
		Location:   abs,
		MountPoint: abs,
		Writable:   f.writable,
	}}
	return g, nil
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

	out, err := g.Run(argv, f.workdir)
	fmt.Print(out)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	g, err := f.guest()
	if err != nil {
		return err
	}
	if err := g.Stop(); err != nil {
		return err
	}
	fmt.Printf("crew-sandbox: %s is stopped\n", g.Name)
	return nil
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
		Port: *port,
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

	// The model token lives in the guest. Reading it here keeps it out of the
	// host's environment and out of the binary's configuration.
	g, err := f.guest()
	if err != nil {
		return err
	}
	if token, err := g.Run([]string{"sudo", "-n", "cat", "/etc/crew-model-token"}, ""); err == nil {
		cfg.ModelToken = trimSpace(token)
	}

	p, err := proxy.Command(ctx, cfg)
	if err != nil {
		return err
	}
	defer p.Cleanup()
	return runForeground(p.Cmd)
}

// cmdCheck runs the verification gates. The gates are not ported yet, so this
// reports that plainly rather than pretending to pass.
func cmdCheck(ctx context.Context, args []string) error {
	return errors.New("check: the verification gates are not ported yet")
}

// secret resolves a name through the vault chain. The environment is checked
// first: an operator who exported the secret has already made the decision, and
// asking the vault would block on an approval prompt for a value we already have.
// Atomic Vault is the fallback and the default for anyone who has not exported it.
func secret(ctx context.Context, name string) (string, error) {
	provider := vault.Chain(vault.FromEnv{}, vault.AtomicVault{})
	value, err := provider.Get(ctx, name)
	if err != nil {
		return "", fmt.Errorf("%s: %w (export it or set it in Atomic Vault)", name, err)
	}
	return value, nil
}

// runForeground runs a child process and waits for it, forwarding signals so a
// Ctrl-C reaches the broker or proxy rather than orphaning it.
func runForeground(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
