// Package allowlist owns the two policy files that decide what the sandbox may
// reach: the hostnames the proxy admits and the repositories the broker will mint
// a token for.
//
// The files live here and nowhere else. The proxy and the broker embed them from
// this package rather than keeping their own copies, so there is one place to edit
// and no way for the two to drift apart.
package allowlist

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
)

// Egress is the hostname allowlist the proxy enforces. A line starting with a dot
// matches that domain and its subdomains; a bare name matches exactly. A `#`
// starts a comment.
//
//go:embed egress-allow
var Egress []byte

// Repos is the repository allowlist the broker enforces. A token is minted only
// for a repository named here. A `#` starts a comment.
//
//go:embed repos-allow
var Repos []byte

// ErrEmpty is returned when a policy file names no entries. An empty allowlist is
// almost always a mistake, and failing loudly is safer than silently denying
// everything or, worse, admitting everything.
var ErrEmpty = errors.New("allowlist is empty")

// Parse splits a policy file into its entries. Blank lines and comments are
// dropped. The format is shared by both files, so one parser serves both.
func Parse(data []byte) []string {
	var entries []string
	for _, line := range strings.Split(string(data), "\n") {
		entry := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// ParseChecked is Parse plus the empty check. Callers that are about to enforce a
// policy should use this so an empty file cannot quietly become "deny nothing".
func ParseChecked(data []byte) ([]string, error) {
	entries := Parse(data)
	if len(entries) == 0 {
		return nil, ErrEmpty
	}
	return entries, nil
}

// HostAllowed reports whether a hostname matches the egress allowlist. A rule
// starting with a dot matches the domain and its subdomains; a bare rule matches
// exactly. The comparison is case-insensitive and ignores a trailing dot, matching
// how the proxy itself decides.
func HostAllowed(hostname string, rules []string) bool {
	host := strings.ToLower(strings.TrimSuffix(hostname, "."))
	for _, rule := range rules {
		rule = strings.ToLower(rule)
		if strings.HasPrefix(rule, ".") {
			if host == rule[1:] || strings.HasSuffix(host, rule) {
				return true
			}
			continue
		}
		if host == rule {
			return true
		}
	}
	return false
}

// RepoAllowed reports whether a repository is named in the broker allowlist. The
// comparison is exact: a repository is a specific owner/name pair, not a pattern.
func RepoAllowed(repo string, rules []string) bool {
	for _, rule := range rules {
		if rule == repo {
			return true
		}
	}
	return false
}

// String renders the entries as a policy file, one per line. It is used when a
// caller supplies its own list and the file has to be staged for a child process.
func String(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	return strings.Join(entries, "\n") + "\n"
}

// Validate checks that a policy file parses and names at least one entry. It
// returns the entries so a caller can use them without parsing twice.
func Validate(data []byte) ([]string, error) {
	entries, err := ParseChecked(data)
	if err != nil {
		return nil, fmt.Errorf("%w: no entries found", err)
	}
	return entries, nil
}
