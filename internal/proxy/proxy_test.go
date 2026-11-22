package proxy

import (
	"context"
	"os"
	"strings"
	"testing"
)

// The script must read the key from a provider-neutral variable. A vendor name in
// the environment would make the gateway DeepSeek-specific again.
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
	if strings.Contains(strings.ToLower(env), "deepseek") {
		t.Errorf("vendor name leaked into the child environment:\n%s", env)
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
