package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromptAgyGoogleLoginSkipsWhenTokenFileExists(t *testing.T) {
	home := withTempHome(t)
	resetAgyGoogleLoginPrompt()
	t.Cleanup(resetAgyGoogleLoginPrompt)

	// A file-backed OAuth token means the user is already signed in: the
	// prompt must return before consuming the once-per-process offer.
	tokenPath := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("cached-oauth-token"), 0600); err != nil {
		t.Fatal(err)
	}

	PromptAgyGoogleLogin("/usr/bin/true")

	if agyGoogleLoginOffered.Load() {
		t.Fatal("present token file must not consume the login-prompt offer")
	}
}

func TestPromptAgyGoogleLoginNonTTYExplainsManualSignIn(t *testing.T) {
	withTempHome(t)
	resetAgyGoogleLoginPrompt()
	t.Cleanup(resetAgyGoogleLoginPrompt)

	// os.Stdin is a pipe, not a terminal: the prompt claims the offer for the
	// process but must not try to launch an interactive `agy`.
	withStdinPipe(t, "")

	PromptAgyGoogleLogin("/usr/bin/true")

	if !agyGoogleLoginOffered.Load() {
		t.Fatal("non-TTY run must still consume the once-per-process offer")
	}
}

func TestPromptAgyGoogleLoginSkipsWhenOfferAlreadyConsumed(t *testing.T) {
	withTempHome(t)
	resetAgyGoogleLoginPrompt()
	t.Cleanup(resetAgyGoogleLoginPrompt)

	if !claimAgyGoogleLoginPrompt() {
		t.Fatal("first claim must succeed")
	}

	withStdinPipe(t, "")
	PromptAgyGoogleLogin("/usr/bin/true")

	if !agyGoogleLoginOffered.Load() {
		t.Fatal("the claim must remain consumed after the skipped prompt")
	}
}
