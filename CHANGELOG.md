# Changelog

All notable changes to `mcpharness` are documented here. The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to [SemVer](https://semver.org).

## [v0.2.0] — 2026-05-27

### Added

- `sdk` subpackage — adapter for [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) (the official Anthropic Go SDK). Uses the SDK's in-memory transport pair to drive a `*mcp.Server` in-process; translates `ClientSession` methods into the SDK-neutral `mcpharness.Client` surface.
- `conformance` subpackage — `conformance.Run(t, url, opts...)` drives Anthropic's official [conformance test harness](https://github.com/modelcontextprotocol/conformance) (`npx @modelcontextprotocol/conformance`) against a running MCP server and fails `t.Fatalf` on any scenario regression. Auto-skips when `npx` is not on PATH. Tolerates both summary-object and NDJSON output formats.
- `conformance.WithSuite` / `WithSkip` / `WithTimeout` / `WithNpxPath` / `WithPackage` / `WithExtraArgs` options for narrowing the run and pinning harness version in CI.

### Changed

- **Minimum Go version bumped to 1.25** (required by `modelcontextprotocol/go-sdk`). Go 1.21+ users get an automatic toolchain download; no manual upgrade needed.
- CI matrix updated to Go 1.25 + 1.26.

[v0.2.0]: https://github.com/ultramcu/mcpharness/releases/tag/v0.2.0

## [v0.1.0] — 2026-05-27

Initial release.

### Added

- `mcpharness.Client` — SDK-neutral interface covering `Initialize`, `ListTools`, `CallTool`, `ListResources`, `ReadResource`, and `Close`.
- `mcpharness.Recorder` — wraps any `Client` and writes every call to a JSON Lines stream.
- `mcpharness.ReplayClient` — reads a recorded stream back and asserts each call matches the recording. Detects wrong method, wrong params, and missing/extra calls.
- `mark3` subpackage — adapter for [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) (the de-facto Go MCP framework).
- CI: `go vet` + `go test -race` on Go 1.23 and 1.24.

[v0.1.0]: https://github.com/ultramcu/mcpharness/releases/tag/v0.1.0
