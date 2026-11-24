package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// addedEnv returns the environment entries Command appended to the child, i.e. the
// ones this package chose. Only those are this package's business: the child also
// inherits the operator's whole environment, which may legitimately name any
// vendor, so scanning the joined environment for a vendor name fails for anyone
// whose shell already mentions one - a DeepSeek API key or a model selector is
// enough - regardless of what the code does.
func addedEnv(cmd *exec.Cmd) []string {
	inherited := make(map[string]bool, len(os.Environ()))
	for _, entry := range os.Environ() {
		inherited[entry] = true
	}
	var added []string
	for _, entry := range cmd.Env {
		if !inherited[entry] {
			added = append(added, entry)
		}
	}
	return added
}

// The script must read the key from a provider-neutral variable. A vendor name in
// the environment this package sets would make the gateway DeepSeek-specific again.
func TestCommandUsesProviderNeutralKeyVariable(t *testing.T) {
	p, err := Command(context.Background(), Config{
		Provider: Provider{Name: "acme", Upstream: "https://api.acme.test/v1"},
		ModelKey: "sk-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cleanup()

	env := strings.Join(p.Cmd.Env, "\n")
	if !strings.Contains(env, "CREW_MODEL_KEY=sk-test") {
		t.Errorf("key not passed as CREW_MODEL_KEY:\n%s", env)
	}
	// Assert on the variables this package sets, not on the inherited environment.
	added := strings.Join(addedEnv(p.Cmd), "\n")
	if strings.Contains(strings.ToLower(added), "deepseek") {
		t.Errorf("vendor name leaked into the child environment this package sets:\n%s", added)
	}
}

// The upstream is a setting. It must reach the script as an argument, and an empty
// upstream must not produce one, so the script's own default (disabled) applies.
func TestCommandPassesUpstreamOnlyWhenSet(t *testing.T) {
	withUpstream, err := Command(context.Background(), Config{
		Provider: Provider{Name: "acme", Upstream: "https://api.acme.test/v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer withUpstream.Cleanup()
	if !containsArg(withUpstream.Cmd.Args, "--model-upstream") {
		t.Errorf("upstream not passed: %v", withUpstream.Cmd.Args)
	}

	without, err := Command(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer without.Cleanup()
	if containsArg(without.Cmd.Args, "--model-upstream") {
		t.Errorf("empty upstream should not be passed: %v", without.Cmd.Args)
	}
}

// A proxy with no provider must still start: egress enforcement does not depend on
// the model gateway.
func TestCommandStartsWithoutProvider(t *testing.T) {
	p, err := Command(context.Background(), Config{})
	if err != nil {
		t.Fatalf("proxy refused to start without a provider: %v", err)
	}
	defer p.Cleanup()
	// Args[0] is the node binary; the staged script is the first argument to it.
	if _, err := os.Stat(p.Cmd.Args[1]); err != nil {
		t.Errorf("staged script missing: %v", err)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// proxy.mjs ships as data, so a syntax error would otherwise surface only when
// the proxy starts against a live guest. `node --check` catches it in the same
// run as the gates' `bash -n`; `nix flake check` executes this.
func TestScriptParses(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}
	path := filepath.Join(t.TempDir(), "proxy.mjs")
	if err := os.WriteFile(path, script, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("node", "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("proxy.mjs does not parse: %v\n%s", err, out)
	}
}

// The model gateway holds the model key, so it must listen on its own port
// rather than on the egress proxy's socket. The guest firewall names both ports,
// and a bug in either route must not be able to reach the other.
func TestGatewayListensSeparatelyFromEgress(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}

	upstream := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer secret" {
				t.Errorf("upstream Authorization = %q, want the host key", got)
			}
			w.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"m"}]}`)
		}),
	)
	defer upstream.Close()

	egressPort, modelPort := freePortPair(t)
	p, err := Command(context.Background(), Config{
		Provider:   Provider{Name: "test", Upstream: upstream.URL},
		ModelKey:   "secret",
		ModelToken: "guest-token",
		Port:       egressPort,
		ModelPort:  modelPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cleanup()

	var stderr bytes.Buffer
	p.Cmd.Stderr = &stderr
	// The test upstream is an httptest TLS server with a self-signed certificate.
	p.Cmd.Env = append(p.Cmd.Env, "NODE_TLS_REJECT_UNAUTHORIZED=0")
	if err := p.Cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = p.Cmd.Process.Kill()
		_ = p.Cmd.Wait()
	}()

	modelBase := fmt.Sprintf("http://127.0.0.1:%d/model", modelPort)
	waitForListener(t, modelBase+"/models")

	if code := status(t, modelBase+"/models", ""); code != http.StatusUnauthorized {
		t.Fatalf("model route without a token = %d, want 401\n%s", code, stderr.String())
	}
	if code := status(t, modelBase+"/models", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("model route with a wrong token = %d, want 401", code)
	}
	if code := status(t, modelBase+"/models", "guest-token"); code != http.StatusOK {
		t.Fatalf("model route with the guest token = %d, want 200\n%s", code, stderr.String())
	}

	// A direct request to the egress port with a model path is not a proxy
	// request (it has no absolute URL), so it must not be served as the gateway.
	egressModel := fmt.Sprintf("http://127.0.0.1:%d/model/models", egressPort)
	if code := status(t, egressModel, "guest-token"); code == http.StatusOK {
		t.Fatalf("the egress proxy answered the model route; the gateway is not isolated")
	}
}

// freePortPair reserves two loopback ports, then releases them for the proxy.
func freePortPair(t *testing.T) (int, int) {
	t.Helper()
	listeners := make([]net.Listener, 0, 2)
	ports := make([]int, 0, 2)
	for i := 0; i < 2; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	return ports[0], ports[1]
}

func waitForListener(t *testing.T, url string) {
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

func status(t *testing.T, url, token string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := testHTTPClient.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	return response.StatusCode
}
