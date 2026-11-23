package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The real GitHub App key is PKCS#1 PEM and spans many lines. Parsing an env dump
// kept only the first line, which made the broker crash on a key Node could not
// decode. The provider must return the value exactly as av handed it over.
func TestAtomicVaultReturnsMultiLineValue(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "av")
	script := `#!/bin/sh
shift 3
export GITHUB_APP_PRIVATE_KEY='-----BEGIN RSA PRIVATE KEY-----
MIIEexample
-----END RSA PRIVATE KEY-----'
exec "$@"
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := AtomicVault{Binary: binary, Timeout: 5 * time.Second}.Get(
		context.Background(),
		"GITHUB_APP_PRIVATE_KEY",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "-----BEGIN RSA PRIVATE KEY-----\nMIIEexample\n-----END RSA PRIVATE KEY-----"
	if got != want {
		t.Fatalf("Get returned %q, want %q", got, want)
	}
}

func TestAtomicVaultRejectsInvalidName(t *testing.T) {
	_, err := (AtomicVault{Binary: "/bin/false"}).Get(context.Background(), "BAD;rm -rf /")
	if err == nil {
		t.Fatal("Get accepted a name that is not an environment variable")
	}
}
