package repl

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/tui"
)

type visionTestTransport func(*http.Request) (*http.Response, error)

func (f visionTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func visionTestResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func TestRunVisionSidecarImageRouting(t *testing.T) {
	for _, command := range []string{"/screenshot", "/paste"} {
		for _, route := range []string{"description", "embedded fallback", "no fallback"} {
			t.Run(command+"/"+route, func(t *testing.T) {
				replEnv(t)
				stubSeams(t)
				img := &tui.ImageAttachment{MediaType: "image/png", Data: "aGk=", FileName: "screen.png"}
				captureScreenshot = func() (*tui.ImageAttachment, error) { return img, nil }
				pasteImage = captureScreenshot
				var sidecarBodies, embeddedBodies []string
				http.DefaultTransport = visionTestTransport(func(req *http.Request) (*http.Response, error) {
					defer req.Body.Close()
					body, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					if req.URL.Host == "sidecar.test" {
						sidecarBodies = append(sidecarBodies, string(body))
						if route == "description" {
							return visionTestResponse(req, http.StatusOK, `{"choices":[{"message":{"content":"[1] screen.png: login error"}}]}`), nil
						}
						return visionTestResponse(req, http.StatusBadRequest, `{"error":{"message":"test sidecar failure"}}`), nil
					}
					embeddedBodies = append(embeddedBodies, string(body))
					// Fail locally after recording the request, so this test proves
					// the fallback sent the image without contacting a real model.
					return visionTestResponse(req, http.StatusBadRequest, `{"error":{"message":"test embedded failure"}}`), nil
				})
				cfg := &api.Config{VisionSidecar: api.VisionSidecarAlways, CerebrasKey: "test-key", CerebrasBaseURL: "https://sidecar.test"}
				ag := newTestAgent(cfg, &api.SessionContext{LocalOnly: true})
				if route == "embedded fallback" {
					ag.Cfg.AnthropicKey = "test-key"
				}
				cli := &fakeCLI{response: "done"}
				out := runREPL(t, ag, cli, command)
				if len(sidecarBodies) != 1 || !strings.Contains(sidecarBodies[0], "data:image/png;base64,aGk=") {
					t.Fatal("the sidecar did not receive the image exactly once")
				}
				switch route {
				case "description":
					if len(cli.prompts) != 1 || !strings.Contains(cli.prompts[0], "<image-descriptions>\n[1] screen.png: login error") || len(embeddedBodies) != 0 {
						t.Fatal("description did not reach the selected CLI exclusively")
					}
				case "embedded fallback":
					assertContains(t, out, "falling back to the built-in multimodal backend")
					if len(cli.prompts) != 0 || len(embeddedBodies) != 1 || !strings.Contains(embeddedBodies[0], `"data":"aGk="`) {
						t.Fatal("fallback lost the image or sent an extra CLI turn")
					}
				case "no fallback":
					assertContains(t, out, "image not sent")
					if len(cli.prompts) != 0 || len(embeddedBodies) != 0 || len(ag.History) != 0 {
						t.Fatal("a failed image turn reached a backend or conversation history")
					}
				}
			})
		}
	}
}

func TestRunVisionSidecarDraggedImageFailureKeepsTextTurn(t *testing.T) {
	home := replEnv(t)
	stubSeams(t)
	path := filepath.Join(home, "dragged.png")
	if err := os.WriteFile(path, []byte("test image"), 0o600); err != nil {
		t.Fatal(err)
	}
	ag := newTestAgent(&api.Config{VisionSidecar: api.VisionSidecarAlways, CerebrasKey: "test-key"}, nil)
	cli := &fakeCLI{response: "done"}
	out := runREPL(t, ag, cli, "inspect "+path)
	assertContains(t, out, "running the turn without image content")
	if len(cli.prompts) != 1 || !strings.Contains(cli.prompts[0], "inspect") || strings.Contains(cli.prompts[0], "<image-descriptions>") {
		t.Fatal("dragged-image failure should preserve the original text-only CLI turn")
	}
}

func TestRunVisionSidecarCancelSkipsBothBackends(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	started := make(chan struct{})
	http.DefaultTransport = visionTestTransport(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	ag := newTestAgent(&api.Config{VisionSidecar: api.VisionSidecarAlways, CerebrasKey: "test-key"}, nil)
	ag.Cfg.AnthropicKey = "test-key" // A configured fallback must also stay idle.
	captureScreenshot = func() (*tui.ImageAttachment, error) {
		return &tui.ImageAttachment{MediaType: "image/png", Data: "aGk=", FileName: "screen.png"}, nil
	}
	runTurnViewport = func(_ *tui.Terminal, _ string, _ *tui.StatusInfo, cancel func(), run func()) tui.TurnInputResult {
		done := make(chan struct{})
		go func() { defer close(done); run() }()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("sidecar request did not start")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("sidecar request ignored cancellation")
		}
		return tui.TurnInputResult{}
	}
	cli := &fakeCLI{response: "done"}
	out := runREPL(t, ag, cli, "/screenshot")
	assertContains(t, out, "Vision sidecar cancelled")
	if len(cli.prompts) != 0 || len(ag.History) != 0 || strings.Contains(out, "falling back") {
		t.Fatal("cancellation must not start a CLI or embedded turn")
	}
}
