// Package update implements the self-update flow: checking the public
// releases repo for a newer version and swapping the running binary in place.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/qualitymax/qmax-code/internal/httpx"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

const (
	releasesRepo  = "Quality-Max/qmax-code-releases"
	checkInterval = 24 * time.Hour
	downloadLimit = 128 << 20 // 128 MiB cap for the asset download
	requestTimeout = 10 * time.Second
)

// latestRelease is a var so tests can point it at a local server.
var latestRelease = "https://api.github.com/repos/" + releasesRepo + "/releases/latest"

// Release describes a newer version available for install.
type Release struct {
	Version string // without the leading "v"
	Asset   string // asset download URL for this platform
}

// checkState persists the last check so users are queried at most once a day.
type checkState struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest,omitempty"`
	Skipped   string    `json:"skipped,omitempty"` // version the user declined
}

func statePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".qmax-code", "update_check.json"), nil
}

func loadState() checkState {
	var st checkState
	p, err := statePath()
	if err != nil {
		return st
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	return st
}

func saveState(st checkState) {
	p, err := statePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	data, _ := json.Marshal(st)
	_ = os.WriteFile(p, data, 0o644)
}

// CompareVersions returns positive when a is newer than b, negative when
// older, zero when equal. Pre-releases (1.24.0-rc1) sort below their release
// (1.24.0); non-semver strings (dev builds: "", "dev") sort below every
// release.
func CompareVersions(a, b string) int {
	aCore, aPre := splitPre(a)
	bCore, bPre := splitPre(b)
	ac, bc := parseSemver(aCore), parseSemver(bCore)
	for i := 0; i < 3; i++ {
		if ac[i] != bc[i] {
			return ac[i] - bc[i]
		}
	}
	// Equal cores: a pre-release is older than the release itself.
	switch {
	case aPre == bPre:
		return 0
	case aPre == "":
		return 1
	default:
		return -1
	}
}

func splitPre(v string) (core, pre string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.Index(v, "-"); idx >= 0 {
		return v[:idx], v[idx+1:]
	}
	return v, ""
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx] // ignore pre-release/build metadata for comparison
	}
	var out [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return out // malformed → zero → sorts below everything valid
		}
		out[i] = n
	}
	return out
}

func isReleaseVersion(v string) bool {
	p := parseSemver(v)
	return p[0] != 0 || p[1] != 0 || p[2] != 0
}

func updateCheckDisabled() bool {
	return os.Getenv("QMAX_NO_UPDATE_CHECK") == "1"
}

// assetName returns the releases-repo asset for the running platform,
// matching the naming produced by the release workflow.
func assetName(goos, goarch string) string {
	name := fmt.Sprintf("qmax-code-%s-%s", goos, goarch)
	if goos == "windows" {
		return name + ".zip"
	}
	return name + ".tar.gz"
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetchLatest retrieves the latest release metadata from the GitHub API.
// Egress goes through internal/httpx so the request is receipt-recorded and
// passes the repo's egress guard.
func fetchLatest(client *http.Client) (*ghRelease, error) {
	if client == nil {
		client = httpx.NewClient(requestTimeout)
	}
	req, err := httpx.NewRequest(context.Background(), http.MethodGet, latestRelease, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "qmax-code-selfupdate")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release check: unexpected status %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("release check: no tag_name in response")
	}
	return &rel, nil
}

// MaybeCheck returns a Release when a newer version exists and the user
// should be asked about it now (respecting the daily cache, the skip marker,
// dev builds, and QMAX_NO_UPDATE_CHECK). Network failures return nil — the
// check must never break startup.
func MaybeCheck(current string) *Release {
	if updateCheckDisabled() || !isReleaseVersion(current) {
		return nil
	}
	st := loadState()
	if time.Since(st.LastCheck) < checkInterval {
		return nil
	}
	rel, err := fetchLatest(nil)
	st.LastCheck = time.Now()
	if err != nil {
		saveState(st) // don't hammer the API on repeated failures either
		return nil
	}
	st.Latest = strings.TrimPrefix(rel.TagName, "v")
	saveState(st)
	if CompareVersions(st.Latest, current) <= 0 {
		return nil
	}
	if st.Skipped == st.Latest {
		return nil // user already declined this exact version
	}
	want := assetName(runtime.GOOS, runtime.GOARCH)
	for _, a := range rel.Assets {
		if a.Name == want {
			return &Release{Version: st.Latest, Asset: a.BrowserDownloadURL}
		}
	}
	return nil
}

// Check forces a check ignoring the daily cache (used by /update), while
// still honoring QMAX_NO_UPDATE_CHECK and dev builds.
func Check(current string) (*Release, error) {
	if updateCheckDisabled() {
		return nil, fmt.Errorf("update checks disabled (QMAX_NO_UPDATE_CHECK=1)")
	}
	if !isReleaseVersion(current) {
		return nil, fmt.Errorf("dev build (%q) — install a release build to self-update", current)
	}
	rel, err := fetchLatest(nil)
	if err != nil {
		return nil, err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if CompareVersions(latest, current) <= 0 {
		return nil, nil // up to date
	}
	want := assetName(runtime.GOOS, runtime.GOARCH)
	for _, a := range rel.Assets {
		if a.Name == want {
			return &Release{Version: latest, Asset: a.BrowserDownloadURL}, nil
		}
	}
	return nil, fmt.Errorf("no release asset for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// MarkSkipped records that the user declined the given version.
func MarkSkipped(version string) {
	st := loadState()
	st.Skipped = version
	saveState(st)
}

// Download fetches the release archive and returns its bytes.
func (r *Release) Download() ([]byte, error) {
	client := httpx.NewClient(5 * time.Minute)
	req, err := httpx.NewRequest(context.Background(), http.MethodGet, r.Asset, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, downloadLimit))
}

// extractBinary pulls the platform binary out of a .tar.gz or .zip archive.
func extractBinary(archive []byte, goos string) ([]byte, error) {
	// Release archives contain the platform-named binary
	// (qmax-code-darwin-arm64, qmax-code-windows-amd64.exe, ...).
	binName := fmt.Sprintf("qmax-code-%s-%s", goos, runtime.GOARCH)
	if goos == "windows" {
		binName += ".exe"
	}
	if goos == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == binName {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, downloadLimit))
			}
		}
		return nil, fmt.Errorf("%s not found in archive", binName)
	}
	gz, err := gzip.NewReader(bytesReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == binName && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, downloadLimit))
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binName)
}

// Apply downloads the release and atomically replaces the binary at exePath.
// The running process is unaffected; a restart picks up the new version.
func (r *Release) Apply(exePath string) error {
	archive, err := r.Download()
	if err != nil {
		return err
	}
	bin, err := extractBinary(archive, runtime.GOOS)
	if err != nil {
		return err
	}
	if len(bin) < 1<<10 {
		return fmt.Errorf("extracted binary suspiciously small (%d bytes)", len(bin))
	}

	// Windows cannot overwrite a running executable in place; shift it aside
	// and drop the new one where it was. The .old file is cleaned by the
	// installer on future runs.
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(exePath); err == nil {
			_ = os.Rename(exePath, exePath+".old")
		}
	}

	tmp := exePath + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, exePath); err != nil {
		_ = os.Remove(tmp)
		// Cross-device installs (binary on another mount): fall back to an
		// in-place rewrite, which works when the file isn't running-locked.
		return os.WriteFile(exePath, bin, 0o755)
	}
	return nil
}
