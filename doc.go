// Command qmax-code is the terminal foundation for a standalone IDE with
// optional, first-class QualityMax connectivity through MCP.
//
// It runs standalone against a local repository or connects to QualityMax,
// and can switch inference between the Anthropic API, Cerebras, Ollama, and
// hosted coding agents (Claude Code, Codex, Antigravity, OpenCode) through
// /orch. It supports local coding, QA work, and exposure receipts. Connected
// workflows add test generation and execution, app crawling, and repository
// review.
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
