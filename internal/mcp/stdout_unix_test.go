//go:build darwin || linux

package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRunServerRedirectsFDStdoutAwayFromJSONRPC(t *testing.T) {
	origStdin := os.Stdin
	origStdout := os.Stdout
	origStderr := os.Stderr

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	stdoutFD := int(outW.Fd())

	restore := func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
		os.Stderr = origStderr
	}
	defer func() {
		restore()
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		_ = errR.Close()
		_ = errW.Close()
	}()

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW

	done := make(chan struct{})
	go func() {
		RunServer("test")
		close(done)
	}()

	if _, err := fmt.Fprintln(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`); err != nil {
		t.Fatal(err)
	}

	stdoutReader := bufio.NewReader(outR)
	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := stdoutReader.ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- line
	}()

	var firstLine string
	select {
	case firstLine = <-lineCh:
	case err := <-errCh:
		t.Fatalf("reading initialize response: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initialize response")
	}

	var msg response
	if err := json.Unmarshal([]byte(firstLine), &msg); err != nil {
		t.Fatalf("initialize response is not JSON-RPC: %v: %q", err, firstLine)
	}

	const stray = "raw fd1 write from lower-level dependency\n"
	if _, err := unix.Write(stdoutFD, []byte(stray)); err != nil {
		t.Fatal(err)
	}

	if err := inW.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunServer to exit")
	}
	restore()

	if err := outW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := errW.Close(); err != nil {
		t.Fatal(err)
	}

	remainingStdout, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	stderrBytes, err := io.ReadAll(errR)
	if err != nil {
		t.Fatal(err)
	}

	if stdout := firstLine + string(remainingStdout); strings.Contains(stdout, stray) {
		t.Fatalf("raw fd stdout leaked onto JSON-RPC stdout: %q", stdout)
	}
	if stderr := string(stderrBytes); !strings.Contains(stderr, strings.TrimSpace(stray)) {
		t.Fatalf("raw fd stdout was not redirected to stderr; stderr = %q", stderr)
	}
}
