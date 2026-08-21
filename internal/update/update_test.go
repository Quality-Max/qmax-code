package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"bytes"
	"fmt"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign of result
	}{
		{"1.24.0", "1.23.0", 1},
		{"1.23.0", "1.24.0", -1},
		{"1.24.0", "1.24.0", 0},
		{"v1.24.0", "1.23.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.24.1", "1.24.0", 1},
		{"1.24.0-rc1", "1.24.0", -1}, // pre-release sorts below release
		{"dev", "1.0.0", -1},
		{"", "1.0.0", -1},
	}
	for _, c := range cases {
		got := CompareVersions(c.a, c.b)
		if (got > 0) != (c.want > 0) || (got < 0) != (c.want < 0) {
			t.Errorf("CompareVersions(%q, %q) = %d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsReleaseVersion(t *testing.T) {
	for _, v := range []string{"1.23.0", "v0.1.2"} {
		if !isReleaseVersion(v) {
			t.Errorf("isReleaseVersion(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "dev", "0.0.0"} {
		if isReleaseVersion(v) {
			t.Errorf("isReleaseVersion(%q) = true, want false", v)
		}
	}
}

func TestAssetName(t *testing.T) {
	if got := assetName("darwin", "arm64"); got != "qmax-code-darwin-arm64.tar.gz" {
		t.Errorf("darwin asset = %q", got)
	}
	if got := assetName("windows", "amd64"); got != "qmax-code-windows-amd64.zip" {
		t.Errorf("windows asset = %q", got)
	}
}

func TestMaybeCheckRespectsCacheAndSkip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows UserHomeDir

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Quality-Max/qmax-code-releases/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v9.9.9",
			"assets": []map[string]string{{
				"name":               assetName(runtime.GOOS, runtime.GOARCH),
				"browser_download_url": "http://example.invalid/asset",
			}},
		})
	}))
	defer srv.Close()
	orig := latestRelease
	latestRelease = srv.URL + "/repos/Quality-Max/qmax-code-releases/releases/latest"
	defer func() { latestRelease = orig }()

	if rel := MaybeCheck("1.23.0"); rel == nil || rel.Version != "9.9.9" {
		t.Fatalf("MaybeCheck = %+v, want 9.9.9", rel)
	}
	// Second call within 24h must hit the cache and return nil.
	if rel := MaybeCheck("1.23.0"); rel != nil {
		t.Fatalf("cached MaybeCheck = %+v, want nil", rel)
	}
	// A declined version must not be re-offered even after cache expiry.
	MarkSkipped("9.9.9")
	st := loadState()
	st.LastCheck = st.LastCheck.AddDate(0, 0, -2)
	saveState(st)
	if rel := MaybeCheck("1.23.0"); rel != nil {
		t.Fatalf("skipped version re-offered: %+v", rel)
	}
}

func TestMaybeCheckDisabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QMAX_NO_UPDATE_CHECK", "1")
	if rel := MaybeCheck("1.23.0"); rel != nil {
		t.Fatalf("MaybeCheck with QMAX_NO_UPDATE_CHECK = %+v", rel)
	}
	if rel := MaybeCheck("dev"); rel != nil {
		t.Fatalf("MaybeCheck on dev build = %+v", rel)
	}
}

func TestMaybeCheckNetworkFailureIsSilent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	orig := latestRelease
	latestRelease = "http://127.0.0.1:0/none" // unreachable
	defer func() { latestRelease = orig }()
	if rel := MaybeCheck("1.23.0"); rel != nil {
		t.Fatalf("MaybeCheck on network failure = %+v, want nil", rel)
	}
}

func makeTarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinaryTarGz(t *testing.T) {
	bin := bytes.Repeat([]byte("x"), 4096)
	member := fmt.Sprintf("qmax-code-%s-%s", "darwin", runtime.GOARCH)
	arch := makeTarGz(t, member, string(bin))
	got, err := extractBinary(arch, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bin) {
		t.Fatalf("extracted %d bytes, want %d", len(got), len(bin))
	}
	// Nested directory layout must still resolve by base name.
	arch = makeTarGz(t, "build/"+fmt.Sprintf("qmax-code-%s-%s", "linux", runtime.GOARCH), string(bin))
	if _, err := extractBinary(arch, "linux"); err != nil {
		t.Fatal(err)
	}
}

func TestExtractBinaryZip(t *testing.T) {
	bin := bytes.Repeat([]byte("y"), 4096)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, _ := zw.Create("qmax-code-windows-amd64.exe")
	fw.Write(bin)
	zw.Close()
	got, err := extractBinary(buf.Bytes(), "windows")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bin) {
		t.Fatalf("extracted %d bytes, want %d", len(got), len(bin))
	}
}

func TestApplySwapsBinary(t *testing.T) {
	newBin := bytes.Repeat([]byte("n"), 8192)
	var archive []byte
	if runtime.GOOS == "windows" {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		fw, _ := zw.Create(fmt.Sprintf("qmax-code-windows-%s.exe", runtime.GOARCH))
		fw.Write(newBin)
		zw.Close()
		archive = buf.Bytes()
	} else {
		archive = makeTarGz(t, fmt.Sprintf("qmax-code-%s-%s", runtime.GOOS, runtime.GOARCH), string(newBin))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	dir := t.TempDir()
	exe := filepath.Join(dir, "qmax-code")
	if err := os.WriteFile(exe, bytes.Repeat([]byte("o"), 2048), 0o755); err != nil {
		t.Fatal(err)
	}

	rel := &Release{Version: "9.9.9", Asset: srv.URL}
	if err := rel.Apply(exe); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBin) {
		t.Fatalf("binary not replaced (%d bytes)", len(got))
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("temp .new file left behind")
	}
}
