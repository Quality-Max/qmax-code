package vnc

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
