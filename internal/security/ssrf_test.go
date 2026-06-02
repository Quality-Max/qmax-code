package security

import "testing"

func TestIsSafePublicURL_Allows(t *testing.T) {
	safe := []string{
		"https://mcp.linear.app/sse",
		"https://example.com",
		"http://example.com:8080/path?q=1",
		"https://sub.domain.co.uk/",
		"https://api.github.com/repos/x/y",
		"https://1example.com",    // starts with a digit but not numeric
		"https://0x1.example.com", // hex-looking label but has a non-IP label
		"https://example.com.",    // trailing root dot on a real host is fine
	}
	for _, u := range safe {
		if !IsSafePublicURL(u) {
			t.Errorf("IsSafePublicURL(%q) = false, want true (should be allowed)", u)
		}
	}
}

func TestIsSafePublicURL_BlocksSchemes(t *testing.T) {
	bad := []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"data:text/html,<script>",
		"gopher://example.com",
		"ftp://example.com/x",
		"ws://example.com/socket",
		"example.com",   // no scheme
		"//example.com", // scheme-relative
		"",              // empty
		"   ",           // whitespace only
	}
	for _, u := range bad {
		if IsSafePublicURL(u) {
			t.Errorf("IsSafePublicURL(%q) = true, want false (bad/missing scheme)", u)
		}
	}
}

func TestIsSafePublicURL_BlocksLocalAndPrivateNames(t *testing.T) {
	bad := []string{
		"http://localhost",
		"http://localhost:3000/x",
		"http://LOCALHOST/x",     // case-insensitive
		"http://localhost./x",    // trailing root dot
		"http://api.localhost/x", // *.localhost
		"http://printer.local/x", // *.local (mDNS)
		"http://service.LOCAL/x",
	}
	for _, u := range bad {
		if IsSafePublicURL(u) {
			t.Errorf("IsSafePublicURL(%q) = true, want false (local/private name)", u)
		}
	}
}

func TestIsSafePublicURL_BlocksLoopbackAndMetadataAndRFC1918(t *testing.T) {
	bad := []string{
		"http://127.0.0.1/x",
		"http://127.1/x", // short-form loopback
		"http://0.0.0.0/x",
		"http://169.254.169.254/latest/meta-data/", // AWS/Azure metadata
		"http://100.100.100.200/x",                 // Alibaba Cloud metadata
		"http://10.0.0.5/x",                        // RFC-1918 class A
		"http://172.16.0.1/x",                      // RFC-1918 class B
		"http://172.31.255.255/x",
		"http://192.168.1.1/x", // RFC-1918 class C
	}
	for _, u := range bad {
		if IsSafePublicURL(u) {
			t.Errorf("IsSafePublicURL(%q) = true, want false (loopback/metadata/private IP)", u)
		}
	}
}

// The headline of QUA-766: alternative IPv4 notations that bypass naive
// substring/dotted-decimal checks.
func TestIsSafePublicURL_BlocksIPv4NotationBypasses(t *testing.T) {
	bad := []string{
		"http://0177.0.0.1/x",   // octal
		"http://0x7f.0.0.1/x",   // hex octet
		"http://0x7f000001/x",   // single hex 32-bit
		"http://2130706433/x",   // bare decimal 32-bit (== 127.0.0.1)
		"http://0/x",            // bare octal zero
		"http://017700000001/x", // octal 32-bit
		"http://192.0x00.0.1/x", // mixed notation
	}
	for _, u := range bad {
		if IsSafePublicURL(u) {
			t.Errorf("IsSafePublicURL(%q) = true, want false (IPv4 notation bypass)", u)
		}
	}
}

func TestIsSafePublicURL_BlocksIPv6Literals(t *testing.T) {
	bad := []string{
		"http://[::1]/x", // IPv6 loopback
		"http://[::1]:8080/x",
		"http://[fd00::1]/x",     // unique local address
		"http://[fe80::1]/x",     // link-local
		"http://[2001:db8::1]/x", // even a "public" v6 literal — we reject all literals
	}
	for _, u := range bad {
		if IsSafePublicURL(u) {
			t.Errorf("IsSafePublicURL(%q) = true, want false (IPv6 literal)", u)
		}
	}
}

func TestIsIPv4Literal(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":   true,
		"127.1":       true,
		"10":          true, // single decimal
		"2130706433":  true, // bare 32-bit
		"0x7f":        true, // hex
		"0177":        true, // octal
		"0":           true, // octal zero
		"1.2.3.4":     true,
		"example":     false, // plain label
		"example.com": false,
		"1example":    false, // digit-led but not numeric
		"0xZZ":        false, // not valid hex
		"1.2.3.4.5":   false, // too many parts
		"":            false, // empty -> single empty part, not numeric
	}
	for in, want := range cases {
		if got := isIPv4Literal(in); got != want {
			t.Errorf("isIPv4Literal(%q) = %v, want %v", in, got, want)
		}
	}
}
