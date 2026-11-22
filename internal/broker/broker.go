// Package broker runs the host-side GitHub token broker. The broker itself is
// broker.mjs, ported from agentic-dev-env; this package is the Go seam that
// supplies its configuration and its secret without either being baked into the
// script.
//
// The broker listens on 127.0.0.1 only. It is an operator tool: the guest reaches
// it through the host gateway, and the guest's nftables rules deliberately do not
// open its port.
package broker

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

//go:embed broker.mjs
var script []byte

//go:embed repos-allow
var defaultRepos []byte

// Config is everything the broker needs to start. The secret is resolved by the
// caller through a vault.Provider and passed in; this package never reads a vault
// itself.
type Config struct {
	// AppID is the GitHub App's numeric ID. It is not a secret.
	AppID string
	// PrivateKey is the App's private key in PEM form. It is a secret and is
	// passed through the environment, never argv.
	PrivateKey string
	// Repos is the allowlist of repositories a token may be minted for. Empty
	// means the embedded default.
	Repos []byte
	// Port is the listen port. Zero means 3129.
	Port int
	// InstallationID pins the installation. Empty means the broker discovers it.
	InstallationID string
	// Node is the node binary. Empty means "node" from PATH.
	Node string
}

// ErrNoPrivateKey is returned when the config carries no key. The broker refuses
// to start without one, so this is caught before the process is spawned.
var ErrNoPrivateKey = errors.New("broker: private key is required")

// Process is a prepared broker invocation. The caller owns the lifecycle, which
// keeps signal handling in one place; Cleanup removes the staged files and must
// be called when the process exits.
type Process struct {
	Cmd     *exec.Cmd
	Cleanup func()
}

// Command builds the broker process without starting it. The staged files live in
// a private temp directory that Cleanup removes, so the key's copy on disk does
// not outlive the run.
func Command(ctx context.Context, cfg Config) (*Process, error) {
	if cfg.PrivateKey == "" {
		return nil, ErrNoPrivateKey
	}
	if cfg.AppID == "" {
		return nil, errors.New("broker: app id is required")
	}

	dir, err := os.MkdirTemp("", "crew-broker-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }

	scriptPath := filepath.Join(dir, "broker.mjs")
	if err := os.WriteFile(scriptPath, script, 0o600); err != nil {
		cleanup()
		return nil, err
	}

	repos := cfg.Repos
	if len(repos) == 0 {
		repos = defaultRepos
	}
	reposPath := filepath.Join(dir, "repos-allow")
	if err := os.WriteFile(reposPath, repos, 0o600); err != nil {
		cleanup()
		return nil, err
	}

	node := cfg.Node
	if node == "" {
		node = "node"
	}
	cmd := exec.CommandContext(ctx, node, scriptPath)
	cmd.Env = append(os.Environ(),
		"CREW_APP_ID="+cfg.AppID,
		"GITHUB_APP_PRIVATE_KEY="+cfg.PrivateKey,
		"CREW_BROKER_REPOS_FILE="+reposPath,
	)
	if cfg.Port != 0 {
		cmd.Env = append(cmd.Env, "CREW_BROKER_PORT="+strconv.Itoa(cfg.Port))
	}
	if cfg.InstallationID != "" {
		cmd.Env = append(cmd.Env, "CREW_INSTALLATION_ID="+cfg.InstallationID)
	}
	return &Process{Cmd: cmd, Cleanup: cleanup}, nil
}

// SelfTest runs the broker's own signing self-test. It proves the key parses and
// the JWT signs without contacting GitHub, so a misconfigured vault is caught
// before the broker is put in front of a guest.
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
		return fmt.Errorf("broker selftest: %w", err)
	}
	return nil
}
