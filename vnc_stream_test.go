package main

import "testing"

func TestNormalizeNoVNCURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// QM Cloud Sandbox exposes ports as https://<port>-<sandboxId>.<domain>
		{"https://6080-abc123.qm-cloud-sndbx.app", "wss://6080-abc123.qm-cloud-sndbx.app/websockify"},
		{"http://localhost:6080", "ws://localhost:6080/websockify"},
		{"wss://6080-abc123.qm-cloud-sndbx.app/websockify", "wss://6080-abc123.qm-cloud-sndbx.app/websockify"},
		{"ws://localhost:6080/websockify", "ws://localhost:6080/websockify"},
		// Trailing slash should still resolve to /websockify
		{"https://6080-abc123.qm-cloud-sndbx.app/", "wss://6080-abc123.qm-cloud-sndbx.app/websockify"},
		// noVNC HTML5 client URL: server returns /vnc.html?autoconnect=true&...
		// Rewrite to /websockify and drop the query so we talk RFB, not HTML.
		{"https://host.example/vnc.html?autoconnect=true&resize=scale", "wss://host.example/websockify"},
		{"https://host.example/vnc_lite.html?autoconnect=true", "wss://host.example/websockify"},
		// Custom path is preserved (some self-hosted noVNC instances move it)
		{"wss://example.com/custom-ws", "wss://example.com/custom-ws"},
	}
	for _, c := range cases {
		got, err := normalizeNoVNCURL(c.in)
		if err != nil {
			t.Errorf("normalizeNoVNCURL(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeNoVNCURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeNoVNCURLRejectsBadScheme(t *testing.T) {
	_, err := normalizeNoVNCURL("ftp://example.com")
	if err == nil {
		t.Fatal("expected error on ftp scheme")
	}
}

func TestBuildIndexMap(t *testing.T) {
	m := buildIndexMap(100, 10)
	if len(m) != 10 {
		t.Fatalf("len = %d, want 10", len(m))
	}
	// First and last should land in source range, monotonically nondecreasing.
	for i, v := range m {
		if v < 0 || v >= 100 {
			t.Errorf("m[%d] = %d out of range", i, v)
		}
		if i > 0 && v < m[i-1] {
			t.Errorf("non-monotonic at %d: %d < %d", i, v, m[i-1])
		}
	}
	// Upsampling: dst > src should still produce valid indices.
	m = buildIndexMap(3, 9)
	if len(m) != 9 {
		t.Fatalf("len = %d, want 9", len(m))
	}
	for i, v := range m {
		if v < 0 || v >= 3 {
			t.Errorf("upsampled m[%d] = %d out of range", i, v)
		}
	}
}
