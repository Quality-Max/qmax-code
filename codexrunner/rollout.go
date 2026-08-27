package codexrunner

import (
	"os"
	"path/filepath"
)

// RolloutLocator resolves the local Codex rollout file backing a validated
// thread ID. It returns an empty path when no rollout can be found, which the
// runner records as "not locatable" rather than as an error: a checkpoint
// without a rollout is still a usable same-sandbox checkpoint.
//
// Implementations must not read or log rollout contents. A rollout is a full
// transcript, and this package never moves transcript data outside Presenter.
type RolloutLocator interface {
	LocateRollout(threadID string) string
}

// RolloutLocatorFunc adapts a function to RolloutLocator.
type RolloutLocatorFunc func(threadID string) string

// LocateRollout implements RolloutLocator.
func (f RolloutLocatorFunc) LocateRollout(threadID string) string { return f(threadID) }

// CodexHomeLocator finds rollouts in a Codex session store. Codex writes one
// rollout per thread under
// sessions/<yyyy>/<mm>/<dd>/rollout-<timestamp>-<thread_id>.jsonl.
type CodexHomeLocator struct {
	// Home overrides the Codex home directory. Empty resolves $CODEX_HOME,
	// then $HOME/.codex.
	Home string
}

// LocateRollout implements RolloutLocator. It returns an absolute path, or ""
// when the store is absent or holds no rollout for threadID.
func (locator CodexHomeLocator) LocateRollout(threadID string) string {
	if !validThreadID(threadID) {
		return ""
	}
	home := locator.Home
	if home == "" {
		home = codexHome()
	}
	if home == "" {
		return ""
	}
	sessions := filepath.Join(home, "sessions")
	// threadID is a validated UUID, so it carries no glob metacharacters.
	name := "rollout-*-" + threadID + ".jsonl"
	for _, pattern := range []string{
		filepath.Join(sessions, "*", "*", "*", name),
		filepath.Join(sessions, name),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			if !regularFile(match) {
				continue
			}
			absolute, err := filepath.Abs(match)
			if err != nil {
				continue
			}
			return absolute
		}
	}
	return ""
}

func codexHome() string {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// validateRolloutPath enforces the two properties a durable rollout reference
// must have to be resumable: it is absolute, so it does not depend on the
// working directory of whichever sandbox restores it, and it exists on this
// box. A checkpoint restored into a replacement sandbox fails the second
// check, which is exactly the handover this validation exists to make loud.
func validateRolloutPath(path string) error {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		return ErrInvalidRolloutPath
	}
	if !regularFile(path) {
		return ErrRolloutUnavailable
	}
	return nil
}
