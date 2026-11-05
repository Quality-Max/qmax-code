// Command qmax-code is a developer-first coding environment: a terminal
// agent that works on the local repository with no account required, and the
// foundation for a standalone IDE with optional, first-class QualityMax
// connectivity through MCP.
//
// In standalone mode (--local) it reads, writes, edits, and searches files
// and runs allowlisted commands and tests, using the Anthropic API, Cerebras,
// or Ollama for inference, or hosting Claude Code, Codex, Antigravity, or
// OpenCode through /orch. Every outbound request is recorded in a signed
// Exposure Receipt. Connecting to QualityMax adds hosted test generation and
// execution, app crawling, repository review, and project management over
// the same MCP server.
//
// Install a release binary:
//
//	curl -sL https://qualitymax.io/static/install-qmax-code.txt | bash
//
// Or build from source with Go 1.27.3 or newer:
//
//	go install github.com/Quality-Max/qmax-code@latest
//
// Run qmax-code --help for flags and the README for slash commands.
package main
