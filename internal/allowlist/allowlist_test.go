package allowlist

import (
	"errors"
	"strings"
	"testing"
)

func TestParseDropsCommentsAndBlanks(t *testing.T) {
	data := []byte(`# a comment
github.com

  .nixos.org  # trailing comment
`)
	got := Parse(data)
	want := []string{"github.com", ".nixos.org"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseCheckedRejectsEmpty(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte(""),
		[]byte("# only a comment\n\n"),
	} {
		if _, err := ParseChecked(data); !errors.Is(err, ErrEmpty) {
			t.Fatalf("ParseChecked(%q) = %v, want ErrEmpty", data, err)
		}
	}
}

func TestHostAllowedExact(t *testing.T) {
	rules := []string{"github.com"}
	if !HostAllowed("github.com", rules) {
		t.Fatal("github.com should match")
	}
	if HostAllowed("api.github.com", rules) {
		t.Fatal("a bare rule must not match a subdomain")
	}
	if HostAllowed("evil.com", rules) {
		t.Fatal("evil.com should not match")
	}
}

func TestHostAllowedDotMatchesSubdomains(t *testing.T) {
	rules := []string{".nixos.org"}
	for _, host := range []string{"nixos.org", "cache.nixos.org", "a.b.nixos.org"} {
		if !HostAllowed(host, rules) {
			t.Fatalf("%s should match .nixos.org", host)
		}
	}
	if HostAllowed("evilnixos.org", rules) {
		t.Fatal("evilnixos.org must not match .nixos.org")
	}
}

func TestHostAllowedCaseAndTrailingDot(t *testing.T) {
	rules := []string{"GitHub.com"}
	if !HostAllowed("github.com.", rules) {
		t.Fatal("case and a trailing dot should not defeat the match")
	}
}

func TestRepoAllowedIsExact(t *testing.T) {
	rules := []string{"crew-broker"}
	if !RepoAllowed("crew-broker", rules) {
		t.Fatal("crew-broker should match")
	}
	if RepoAllowed("crew-broker-extra", rules) {
		t.Fatal("a repository rule must not match a prefix")
	}
}

func TestEmbeddedFilesAreValid(t *testing.T) {
	// The embedded defaults are what a build with no configuration uses. If either
	// is empty or unparseable, the proxy or broker would refuse to start.
	if _, err := Validate(Egress); err != nil {
		t.Fatalf("embedded egress allowlist is invalid: %v", err)
	}
	if _, err := Validate(Repos); err != nil {
		t.Fatalf("embedded repo allowlist is invalid: %v", err)
	}
}

func TestEmbeddedEgressNamesTheGitHubAPI(t *testing.T) {
	// A regression guard: the API host was once missing from the allowlist, which
	// broke every gh call while the proxy looked healthy.
	if !HostAllowed("api.github.com", Parse(Egress)) {
		t.Fatal("api.github.com is not in the embedded egress allowlist")
	}
}

func TestStringRoundTrips(t *testing.T) {
	entries := []string{"github.com", ".nixos.org"}
	got := Parse([]byte(String(entries)))
	if strings.Join(got, ",") != strings.Join(entries, ",") {
		t.Fatalf("round trip changed the entries: %v", got)
	}
}
