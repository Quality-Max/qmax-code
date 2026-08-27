// Package codexrunner executes Codex CLI turns without depending on qmax-code's
// terminal or on any private cloud implementation.
//
// A first turn invokes "codex exec [--model <model>] --json -". A continued
// turn invokes "codex exec resume [--model <model>] --json <thread_id> -" with
// a validated, explicit thread ID. When a model is supplied, it is validated
// against an exact allowlist before process start and is preserved in durable
// checkpoints. Prompts are supplied only through stdin. Raw provider events
// and diagnostics are never exposed through the structural event interfaces.
//
// Because "codex exec resume" resolves a thread from the local Codex session
// store, a checkpoint also records the path of the rollout backing its thread.
// The package locates that file and nothing more: it never reads, uploads, or
// logs a rollout. Hosts that move a session between sandboxes are responsible
// for shipping the file and for restoring it before they resume, and
// Continuity.Restore refuses a checkpoint whose rollout is not present rather
// than resuming into a thread this box cannot reconstruct.
package codexrunner
