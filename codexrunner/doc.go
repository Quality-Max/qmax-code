// Package codexrunner executes Codex CLI turns without depending on qmax-code's
// terminal or on any private cloud implementation.
//
// A first turn always invokes "codex exec --json -". A continued turn always
// invokes "codex exec resume --json <thread_id> -" with a validated, explicit
// thread ID. Prompts are supplied only through stdin. Raw provider events and
// diagnostics are never exposed through the structural event interfaces.
package codexrunner
