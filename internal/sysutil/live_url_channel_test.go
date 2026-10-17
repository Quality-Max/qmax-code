package sysutil

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// resetLiveURLFile isolates the process-wide singleton path cache so each test
// computes a fresh path under its own HOME override, and re-resets afterwards
// so later tests start clean. No test using this may run in parallel because
// it mutates HOME.
func resetLiveURLFile(t *testing.T) {
	t.Helper()
	ResetLiveURLFileForTest()
	t.Cleanup(ResetLiveURLFileForTest)
}

// resetExecIDFile is the execID equivalent of resetLiveURLFile.
func resetExecIDFile(t *testing.T) {
	t.Helper()
	ResetExecIDFileForTest()
	t.Cleanup(ResetExecIDFileForTest)
}

func TestLiveURLFilePathUnderHome(t *testing.T) {
	resetLiveURLFile(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := LiveURLFilePath()
	if got == "" {
		t.Fatal("LiveURLFilePath() = empty with HOME set")
	}
	if dir := filepath.Dir(got); dir != filepath.Join(home, ".qmax-code") {
		t.Errorf("path dir = %q, want %q", dir, filepath.Join(home, ".qmax-code"))
	}
	wantBase := ".live-url-" + strconv.Itoa(os.Getpid())
	if base := filepath.Base(got); base != wantBase {
		t.Errorf("path base = %q, want %q", base, wantBase)
	}
	if fi, err := os.Stat(filepath.Join(home, ".qmax-code")); err != nil || !fi.IsDir() {
		t.Errorf(".qmax-code directory not created: %v", err)
	}

	// The path is computed once per process: changing HOME must not change it.
	t.Setenv("HOME", t.TempDir())
	if again := LiveURLFilePath(); again != got {
		t.Errorf("LiveURLFilePath() = %q after HOME change, want cached %q", again, got)
	}
}

func TestLiveURLFilePathNoHome(t *testing.T) {
	resetLiveURLFile(t)
	t.Setenv("HOME", "")
	if got := LiveURLFilePath(); got != "" {
		t.Errorf("LiveURLFilePath() = %q with no HOME, want empty", got)
	}
}

func TestPersistLiveURLForParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live-url")
	t.Setenv("QMAX_LIVE_URL_FILE", path)

	// Empty URL is a no-op even with the env var set.
	PersistLiveURLForParent("")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file created for empty URL: %v", err)
	}

	PersistLiveURLForParent("https://6080-sandbox.example/#")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("side-channel file not written: %v", err)
	}
	if string(data) != "https://6080-sandbox.example/#" {
		t.Errorf("file content = %q", string(data))
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("atomic-write tmp file left behind")
	}

	// Without the env var (empty after TrimSpace) nothing is persisted.
	t.Setenv("QMAX_LIVE_URL_FILE", "")
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	PersistLiveURLForParent("https://other.example/")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("persisted without QMAX_LIVE_URL_FILE")
	}
}

func TestPersistLiveURLForParentWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live-url")
	t.Setenv("QMAX_LIVE_URL_FILE", path)

	// Block the atomic-write tmp path with a directory: the tmp write fails
	// and the error must be swallowed (best-effort channel).
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	PersistLiveURLForParent("https://sandbox.example/")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("target file created despite write failure")
	}
}

func TestDrainLiveURLFromChild(t *testing.T) {
	resetLiveURLFile(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := DrainLiveURLFromChild(); got != "" {
		t.Errorf("drain with no file = %q, want empty", got)
	}

	path := LiveURLFilePath()
	if err := os.WriteFile(path, []byte("  https://sandbox.example/feed \n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := DrainLiveURLFromChild(); got != "https://sandbox.example/feed" {
		t.Errorf("drain = %q, want trimmed URL", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("side-channel file not removed after drain")
	}
	if got := DrainLiveURLFromChild(); got != "" {
		t.Errorf("second drain = %q, want empty", got)
	}
}

func TestDrainLiveURLFromChildNoHome(t *testing.T) {
	resetLiveURLFile(t)
	t.Setenv("HOME", "")
	if got := DrainLiveURLFromChild(); got != "" {
		t.Errorf("drain with no HOME = %q, want empty", got)
	}
}

func TestExecIDFilePathUnderHome(t *testing.T) {
	resetExecIDFile(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := ExecIDFilePath()
	if got == "" {
		t.Fatal("ExecIDFilePath() = empty with HOME set")
	}
	wantBase := ".exec-id-" + strconv.Itoa(os.Getpid())
	if base := filepath.Base(got); base != wantBase {
		t.Errorf("path base = %q, want %q", base, wantBase)
	}

	t.Setenv("HOME", t.TempDir())
	if again := ExecIDFilePath(); again != got {
		t.Errorf("ExecIDFilePath() = %q after HOME change, want cached %q", again, got)
	}
}

func TestExecIDFilePathNoHome(t *testing.T) {
	resetExecIDFile(t)
	t.Setenv("HOME", "")
	if got := ExecIDFilePath(); got != "" {
		t.Errorf("ExecIDFilePath() = %q with no HOME, want empty", got)
	}
}

func TestPersistExecIDForParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exec-id")
	t.Setenv("QMAX_EXEC_ID_FILE", path)

	PersistExecIDForParent("")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file created for empty exec ID: %v", err)
	}

	PersistExecIDForParent("exec_12345")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("side-channel file not written: %v", err)
	}
	if string(data) != "exec_12345" {
		t.Errorf("file content = %q", string(data))
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("atomic-write tmp file left behind")
	}

	t.Setenv("QMAX_EXEC_ID_FILE", "")
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	PersistExecIDForParent("exec_67890")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("persisted without QMAX_EXEC_ID_FILE")
	}
}

func TestPersistExecIDForParentWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exec-id")
	t.Setenv("QMAX_EXEC_ID_FILE", path)

	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	PersistExecIDForParent("exec_12345")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("target file created despite write failure")
	}
}

func TestDrainExecIDFromChild(t *testing.T) {
	resetExecIDFile(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := DrainExecIDFromChild(); got != "" {
		t.Errorf("drain with no file = %q, want empty", got)
	}

	path := ExecIDFilePath()
	if err := os.WriteFile(path, []byte("exec_abc123\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := DrainExecIDFromChild(); got != "exec_abc123" {
		t.Errorf("drain = %q, want exec_abc123", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("side-channel file not removed after drain")
	}
	if got := DrainExecIDFromChild(); got != "" {
		t.Errorf("second drain = %q, want empty", got)
	}
}

func TestDrainExecIDFromChildNoHome(t *testing.T) {
	resetExecIDFile(t)
	t.Setenv("HOME", "")
	if got := DrainExecIDFromChild(); got != "" {
		t.Errorf("drain with no HOME = %q, want empty", got)
	}
}
