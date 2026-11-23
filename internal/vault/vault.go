// Package vault abstracts where host-side secrets come from. The broker and the
// proxy need a GitHub App private key and a model API key; neither should know
// whether those live in Atomic Vault, a keychain, or a test fixture.
//
// Atomic Vault is the default backend because it is what the operator already
// uses, but it is reached through the Provider interface so a test can supply a
// static map and a different operator can supply a different backend without
// touching the broker or the proxy.
package vault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// A secret name becomes part of a shell command below, so it is constrained to
// what an environment variable may be called.
var secretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Provider resolves a secret by name. Implementations must not log the value.
type Provider interface {
	// Get returns the secret's value. A missing secret is an error, never an
	// empty string: an empty credential is worse than a loud failure because it
	// looks like a working configuration.
	Get(ctx context.Context, name string) (string, error)
}

// ErrNotFound is returned when the provider has no secret under that name.
var ErrNotFound = errors.New("secret not found")

// AtomicVault reads secrets through the `av` CLI. It is the default provider.
//
// The value is read into memory and handed to the child process's environment;
// it is never written to disk and never appears in argv, so it does not show up
// in `ps` output.
type AtomicVault struct {
	// Binary is the av executable. Empty means "av" from PATH.
	Binary string
	// Timeout bounds the av call. Zero means 10 seconds.
	Timeout time.Duration
}

func (a AtomicVault) binary() string {
	if a.Binary != "" {
		return a.Binary
	}
	return "av"
}

func (a AtomicVault) timeout() time.Duration {
	if a.Timeout > 0 {
		return a.Timeout
	}
	return 10 * time.Second
}

// Get runs `av inject +NAME -- sh -c 'printf %s "$NAME"'` and returns what that
// command writes. av is the only supported way to read a secret without the
// operator pasting it, and it is what the original crew-sandbox.sh used.
//
// Reading the command's stdout avoids parsing an `env` dump, which is ambiguous
// for a multi-line value: a GitHub App private key spans many lines, and a
// line-oriented parser keeps only the first one.
//
// A refusal that means "this secret is not available here" — approval required in
// a non-interactive run, or no such secret — is reported as ErrNotFound so a chain
// can fall through to the next provider. A genuine failure (av missing, a crash)
// is reported as itself and stops the chain.
func (a AtomicVault) Get(ctx context.Context, name string) (string, error) {
	if !secretName.MatchString(name) {
		return "", fmt.Errorf("vault: invalid secret name %q", name)
	}

	// av can block on an interactive approval prompt. A bounded timeout keeps a
	// non-interactive run from hanging forever; the timeout is reported as
	// unavailable so a chain can fall through instead of failing hard.
	ctx, cancel := context.WithTimeout(ctx, a.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, a.binary(), "inject", "+"+name, "--",
		"sh", "-c", `printf '%s' "$`+name+`"`)
	// Show av's own prompt on stderr while keeping it for the error message.
	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%w: %s (av timed out)", ErrNotFound, name)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			msg := strings.TrimSpace(stderr.String())
			if unavailable(msg) {
				return "", fmt.Errorf("%w: %s (%s)", ErrNotFound, name, msg)
			}
			if msg == "" {
				msg = fmt.Sprintf("exit %d", exitErr.ExitCode())
			}
			return "", fmt.Errorf("av inject %s: %s", name, msg)
		}
		return "", fmt.Errorf("av inject %s: %w", name, err)
	}
	return string(out), nil
}

// unavailable reports whether av's message means "not available in this context"
// rather than "something is broken". Approval cannot be granted in a
// non-interactive run, so it is a fall-through, not a failure.
func unavailable(msg string) bool {
	lower := strings.ToLower(msg)
	for _, phrase := range []string{
		"human approval required",
		"not found",
		"no such secret",
		"unknown secret",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// Static is a provider backed by a map. It exists for tests and for operators who
// export secrets into their own environment; it is never the default.
type Static map[string]string

func (s Static) Get(_ context.Context, name string) (string, error) {
	value, ok := s[name]
	if !ok || value == "" {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return value, nil
}

// FromEnv reads a secret from the process environment. It is the fallback when no
// provider is configured, so a developer can run the broker without a vault.
type FromEnv struct{}

func (FromEnv) Get(_ context.Context, name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return value, nil
}

// Chain tries each provider in order and returns the first value found. A
// provider that fails for a reason other than ErrNotFound stops the chain: a
// broken vault should not silently fall through to an environment variable.
func Chain(providers ...Provider) Provider {
	return chain(providers)
}

type chain []Provider

func (c chain) Get(ctx context.Context, name string) (string, error) {
	var lastErr error = ErrNotFound
	for _, p := range c {
		value, err := p.Get(ctx, name)
		if err == nil {
			return value, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return "", err
		}
		lastErr = err
	}
	return "", lastErr
}
