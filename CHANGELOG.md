# Changelog

All notable changes to `mcpharness` are documented here. The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to [SemVer](https://semver.org).

## [v0.1.0] — 2026-05-27

Initial release.

### Added

- `mcpharness.Client` — SDK-neutral interface covering `Initialize`, `ListTools`, `CallTool`, `ListResources`, `ReadResource`, and `Close`.
- `mcpharness.Recorder` — wraps any `Client` and writes every call to a JSON Lines stream.
- `mcpharness.ReplayClient` — reads a recorded stream back and asserts each call matches the recording. Detects wrong method, wrong params, and missing/extra calls.
- `mark3` subpackage — adapter for [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) (the de-facto Go MCP framework).
- CI: `go vet` + `go test -race` on Go 1.23 and 1.24.

[v0.1.0]: https://github.com/ultramcu/mcpharness/releases/tag/v0.1.0
