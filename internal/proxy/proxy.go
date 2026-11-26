// Package proxy runs the host-side egress proxy and model gateway. The proxy
// itself is proxy.mjs, ported from agentic-dev-env; this package is the Go seam
// that supplies its allowlist and its model key without either being baked into
// the script.
//
// The proxy listens on 127.0.0.1 only. The guest reaches it through the host
// gateway on the port the guest's firewall opens.
package proxy

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/aditya-sathish-rao/crew-sandbox/internal/allowlist"
)

//go:embed proxy.mjs
var script []byte

// Provider describes the model provider the gateway forwards to. It is a setting,
// not a constant: the operator names the upstream, so no vendor is hardcoded here.
// The surface is not general, though - the script serves GET /models and
// POST /chat/completions with Authorization: Bearer over https, which is the
// OpenAI-compatible shape. A provider that authenticates differently needs a change
// in the script, not a different flag. The secret that authenticates it is resolved
// by the caller and passed as Config.ModelKey; this package never reads a vault.
type Provider struct {
	// Name is a label used in logs. Empty means "model".
	Name string
	// Upstream is the base URL requests are forwarded to. Empty disables the
	// model gateway, which is the safe default: a proxy without a provider still
	// enforces egress.
	Upstream string
}

// Config is everything the proxy needs to start. The model key is resolved by the
// caller through a vault.Provider and passed in; this package never reads a vault
// itself.
type Config struct {
	// Allow is the egress allowlist. Empty means the embedded default in
	// internal/allowlist.
	Allow []byte
	// Provider is the model provider. An empty Upstream disables the gateway.
	Provider Provider
	// ModelKey is the upstream model API key. Empty disables the model gateway,
	// which is the safe default: a proxy without a key still enforces egress.
	ModelKey string
	// ModelToken is the guest's model token. Empty disables the model gateway.
	ModelToken string
	// Port is the listen port for the CONNECT and plain-HTTP egress proxy. Zero
	// means 3128.
	Port int
	// ModelPort is the model gateway's own listen port. Zero means 3130. It is a
	// separate listener from Port so the gateway, which holds the model key, and
	// the egress proxy are never the same socket.
	ModelPort int
	// KeyHashPath, when set, receives the SHA-256 of the model key. The model gate
	// compares that fingerprint against every value in the guest's environment, so
	// it can prove the key is absent without the key ever reaching the guest.
	KeyHashPath string
	// Node is the node binary. Empty means "node" from PATH.
	Node string
}

// Process is a prepared proxy invocation. The caller owns the lifecycle, which
// keeps signal handling in one place; Cleanup removes the staged files and must be
// called when the process exits.
type Process struct {
	Cmd     *exec.Cmd
	Cleanup func()
}

// Command builds the proxy process without starting it. The staged files live in a
// private temp directory that Cleanup removes.
func Command(ctx context.Context, cfg Config) (*Process, error) {
	dir, err := os.MkdirTemp("", "crew-proxy-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }

	scriptPath := filepath.Join(dir, "proxy.mjs")
	if err := os.WriteFile(scriptPath, script, 0o600); err != nil {
		cleanup()
		return nil, err
	}

	allow := cfg.Allow
	if len(allow) == 0 {
		allow = allowlist.Egress
	}
	// An empty allowlist would start a proxy that refuses every request, which
	// reads as a broken proxy rather than a missing policy. Refuse to start.
	if _, err := allowlist.Validate(allow); err != nil {
		cleanup()
		return nil, fmt.Errorf("proxy: %w", err)
	}
	allowPath := filepath.Join(dir, "egress-allow")
	if err := os.WriteFile(allowPath, allow, 0o600); err != nil {
		cleanup()
		return nil, err
	}

	node := cfg.Node
	if node == "" {
		node = "node"
	}
	port := cfg.Port
	if port == 0 {
		port = 3128
	}
	modelPort := cfg.ModelPort
	if modelPort == 0 {
		modelPort = 3130
	}
	args := []string{
		scriptPath,
		"--listen", strconv.Itoa(port),
		"--model-listen", strconv.Itoa(modelPort),
		"--allow", allowPath,
	}
	if cfg.Provider.Upstream != "" {
		args = append(args, "--model-upstream", cfg.Provider.Upstream)
	}
	if cfg.Provider.Name != "" {
		args = append(args, "--model-name", cfg.Provider.Name)
	}
	if cfg.KeyHashPath != "" {
		args = append(args, "--key-hash-out", cfg.KeyHashPath)
	}

	cmd := exec.CommandContext(ctx, node, args...)
	cmd.Env = append(os.Environ(), "CREW_MODEL_TOKEN="+cfg.ModelToken)
	if cfg.ModelKey != "" {
		// The script reads the key from a fixed variable so the Go layer can pass
		// it without the script knowing which provider it belongs to. The name is
		// the seam, not a vendor: CREW_MODEL_KEY is set from whatever secret the
		// caller resolved.
		cmd.Env = append(cmd.Env, "CREW_MODEL_KEY="+cfg.ModelKey)
	}
	return &Process{Cmd: cmd, Cleanup: cleanup}, nil
}

// SelfTest runs the proxy's ClientHello parser self-test. It proves the allowlist
// parser and the SNI check without a network or a VM, so a broken build is caught
// before the proxy is put in front of a guest.
func SelfTest(ctx context.Context, cfg Config) error {
	p, err := Command(ctx, cfg)
	if err != nil {
		return err
	}
	defer p.Cleanup()
	p.Cmd.Args = append(p.Cmd.Args, "--selftest")
	p.Cmd.Stdout = os.Stderr
	p.Cmd.Stderr = os.Stderr
	if err := p.Cmd.Run(); err != nil {
		return fmt.Errorf("proxy selftest: %w", err)
	}
	return nil
}
