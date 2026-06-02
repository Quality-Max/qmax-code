package security

import (
	"net/url"
	"regexp"
	"strings"
)

// SSRF host validation for user-supplied OUTBOUND URLs (e.g. an MCP server URL,
// or any "fetch this URL" surface). Ported from the e2b coding agent's
// lib/mcp.ts (isSafeSSEUrl) and lib/preview.ts (isDisallowedHost / isIpv4Literal)
// (QUA-766).
//
// NOTE: this is preparatory — qmax-code is currently only an MCP *server* and has
// no outbound user-URL surface, so there is no call site yet. Wire IsSafePublicURL
// in at the point any user-supplied outbound URL is first accepted.
//
// The rule deliberately rejects EVERY IP literal (in every notation) rather than
// computing per-range membership. Refusing raw IPs outright closes private-range
// access together with the octal/hex/decimal/IPv6 notation bypasses
// (0177.0.0.1, 0x7f.0.0.1, 2130706433, [::1], …) in one rule. DNS rebinding is out
// of scope: we validate the literal host, we don't resolve names.

var (
	// ipv4HexPart matches a hex octet such as 0x7f (host is lower-cased first).
	ipv4HexPart = regexp.MustCompile(`^0x[0-9a-f]+$`)
	// ipv4OctalPart matches an octal octet such as 0177, and bare 0.
	ipv4OctalPart = regexp.MustCompile(`^0[0-7]*$`)
	// ipv4DecimalPart matches a decimal octet such as 127 (no leading zero).
	ipv4DecimalPart = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// IsSafePublicURL reports whether raw is an http(s) URL pointing at a public
// hostname. It rejects:
//   - non-http(s) schemes (file:, javascript:, data:, gopher:, …)
//   - localhost, *.localhost, *.local, and the empty host
//   - every IPv6 literal ([::1], fd00::, …)
//   - every IPv4 literal in any resolver-accepted notation (dotted decimal/octal/
//     hex and the short a / a.b / a.b.c forms, plus a bare 32-bit integer)
//
// A non-IP public hostname (example.com, mcp.linear.app, …) is allowed.
func IsSafePublicURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname() // strips port and IPv6 brackets
	if host == "" {
		return false
	}
	return !isDisallowedHost(host)
}

// isDisallowedHost reports whether hostname must be blocked as an SSRF target.
func isDisallowedHost(hostname string) bool {
	// Strip a trailing FQDN-root dot so "localhost." / "app.local." can't slip
	// past the name checks.
	host := strings.ToLower(strings.TrimRight(hostname, "."))
	if host == "" {
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	// url.Hostname() already removes IPv6 brackets, but strip defensively in case
	// a raw host is passed in. Any colon means an IPv6 literal.
	h := strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if strings.Contains(h, ":") {
		return true // any IPv6 literal
	}
	return isIPv4Literal(h) // any IPv4 notation
}

// isIPv4Literal reports whether host is an IPv4 address in any notation a resolver
// would accept — dotted decimal/octal/hex (a.b.c.d and the short a, a.b, a.b.c
// forms) or a bare 32-bit number. Real hostnames have at least one non-numeric
// label, so they never match.
func isIPv4Literal(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if !ipv4HexPart.MatchString(p) && !ipv4OctalPart.MatchString(p) && !ipv4DecimalPart.MatchString(p) {
			return false
		}
	}
	return true
}
