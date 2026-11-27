package broker

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// broker.mjs ships as data, so a syntax error would otherwise surface only when
// the broker starts with a real key. `node --check` catches it in the same run as
// the gates' `bash -n`; `nix flake check` executes this.
func TestScriptParses(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}
	path := filepath.Join(t.TempDir(), "broker.mjs")
	if err := os.WriteFile(path, script, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("node", "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("broker.mjs does not parse: %v\n%s", err, out)
	}
}

// A request the handler cannot parse must cost one response, not the process.
// Every token the broker minted lives only in its memory, so a crash strands
// credentials that can no longer be revoked. A malformed percent-escape in the
// revoke path is the cheapest way to make the handler throw.
func TestBadRequestDoesNotKillTheBroker(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}

	port := freePort(t)
	p, err := Command(context.Background(), Config{
		AppID:      "12345",
		PrivateKey: testPrivateKey(t),
		Repos:      []byte("aditya-sathish-rao/crew-sandbox\n"),
		Port:       port,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cleanup()

	var stderr syncBuffer
	p.Cmd.Stderr = &stderr
	if err := p.Cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = p.Cmd.Process.Kill()
		_ = p.Cmd.Wait()
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHealth(t, base+"/health")

	// %C0%AF is a well-formed escape sequence that decodes to an overlong UTF-8
	// encoding, which decodeURIComponent rejects. Go's URL parser accepts it, so
	// the request reaches the handler and throws there.
	request, err := http.NewRequest(http.MethodDelete, base+"/token/%C0%AF", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := testHTTPClient.Do(request)
	if err != nil {
		t.Fatalf("DELETE malformed revoke path: %v\n%s", err, stderr.String())
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("malformed revoke path = %d, want 500\n%s", response.StatusCode, stderr.String())
	}

	// The process must still be serving: a dead broker refuses the connection.
	response, err = testHTTPClient.Get(base + "/health")
	if err != nil {
		t.Fatalf("the broker died on a bad request: %v\n%s", err, stderr.String())
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health after a bad request = %d, want 200", response.StatusCode)
	}
}

// testPrivateKey mints a throwaway RSA key so the broker's startup validation
// passes without a vault.
func testPrivateKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func waitForHealth(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := testHTTPClient.Get(url)
		if err == nil {
			_ = response.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not start listening", url)
}

var testHTTPClient = &http.Client{Timeout: 5 * time.Second}

// syncBuffer collects the broker's stderr so a failure can quote it. The test
// reads it only after the process is gone, so no lock is needed.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
