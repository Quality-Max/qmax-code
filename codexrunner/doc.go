// Package codexrunner executes Codex CLI turns without depending on qmax-code's
// terminal or on any private cloud implementation.
//
// A first turn invokes "codex exec [--model <model>] --json -". A continued
// turn invokes "codex exec resume [--model <model>] --json <thread_id> -" with
// a validated, explicit thread ID. When a model is supplied, it is validated
// against an exact allowlist before process start and is preserved in durable
// checkpoints. Prompts are supplied only through stdin. Raw provider events
// and diagnostics are never exposed through the structural event interfaces.
package codexrunner
