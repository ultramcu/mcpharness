# mcpharness

[![Go Reference](https://pkg.go.dev/badge/github.com/ultramcu/mcpharness.svg)](https://pkg.go.dev/github.com/ultramcu/mcpharness)
[![CI](https://github.com/ultramcu/mcpharness/actions/workflows/ci.yml/badge.svg)](https://github.com/ultramcu/mcpharness/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/ultramcu/mcpharness)](https://goreportcard.com/report/github.com/ultramcu/mcpharness)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Testing toolkit for Go MCP server authors.**

The [Model Context Protocol (MCP)](https://modelcontextprotocol.io) ecosystem in Go has two competing server frameworks ([`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) and [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk)) plus a growing set of domain-specific MCP servers (GitHub, Grafana, Kubernetes, Terraform, …). Every project ends up hand-rolling roughly the same test plumbing: an in-process client to drive the server, a way to record real sessions for regression tests, an assertion harness for tool behaviour.

`mcpharness` fills that gap with a small SDK-neutral surface.

## Features (v0.1)

- **`mcpharness.Client`** — neutral interface every adapter implements. Today's adapter is [`mark3`](./mark3) for `mark3labs/mcp-go`. An adapter for the official `modelcontextprotocol/go-sdk` is on the v0.2 roadmap.
- **`Recorder`** wraps any `Client` and writes every call (`initialize`, `tools/list`, `tools/call`, `resources/list`, `resources/read`) to a JSON Lines stream.
- **`Replay`** reads a recorded stream back and returns a deterministic `Client` that asserts each call matches the recording. Catches three regression classes: wrong method, wrong params, extra/missing calls.

## Why not just use the framework's own client?

You can, and you should for simple smoke tests. `mcpharness` exists for the moments when one client isn't enough:

- **Test against multiple framework versions** without rewriting tests — the neutral `Client` interface stays put when the underlying framework's request types churn.
- **Record once, replay forever** — capture a real session against your production server, commit the file, then run deterministic regression tests in CI without standing up the real server.
- **Catch divergence early** — `Replay` fails loudly on wrong method, wrong params, or missing/extra calls, so a behaviour drift surfaces as a precise test failure rather than a silent wrong assertion.

## Install

```bash
go get github.com/ultramcu/mcpharness@latest
```

## Quick start (mark3labs adapter)

```go
package mcpserver_test

import (
    "context"
    "testing"

    "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"
    "github.com/ultramcu/mcpharness"
    "github.com/ultramcu/mcpharness/mark3"
)

func TestEcho(t *testing.T) {
    srv := server.NewMCPServer("echo", "0.1.0")
    srv.AddTool(
        mcp.NewTool("echo", mcp.WithString("text", mcp.Required())),
        func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
            text, _ := req.Params.Arguments.(map[string]any)["text"].(string)
            return mcp.NewToolResultText(text), nil
        },
    )

    client, err := mark3.New(srv)
    if err != nil { t.Fatal(err) }
    defer client.Close()

    if _, err := client.Initialize(context.Background()); err != nil {
        t.Fatal(err)
    }
    res, err := client.CallTool(context.Background(), "echo", map[string]any{"text": "ping"})
    if err != nil { t.Fatal(err) }
    if res.IsError { t.Fatal("tool returned IsError") }
    // res.Content[0] == map[string]any{"type":"text", "text":"ping"}
}
```

## Record and replay

```go
// Phase 1: record a real session into testdata/echo.jsonl
func TestRecord(t *testing.T) {
    f, _ := os.Create("testdata/echo.jsonl")
    defer f.Close()

    real, _ := mark3.New(buildServer(t))
    rec := mcpharness.NewRecorder(real, f)
    defer rec.Close()

    rec.Initialize(ctx)
    rec.CallTool(ctx, "echo", map[string]any{"text": "ping"})
}

// Phase 2: in CI, replay deterministically without the real server
func TestReplay(t *testing.T) {
    f, _ := os.Open("testdata/echo.jsonl")
    defer f.Close()

    replay := mcpharness.NewReplay(t, f)
    defer replay.Close()  // fails the test if any recorded entries were not consumed

    replay.Initialize(ctx)                                       // asserts seq=1 matches
    replay.CallTool(ctx, "echo", map[string]any{"text": "ping"}) // asserts seq=2 matches
}
```

If the second-phase test calls a method that doesn't match the recording, or passes different params, `Replay` calls `t.Fatalf` with a precise diff — no silent drift.

## Roadmap

- **v0.1** (this release): `Client` + `Recorder` + `Replay` + mark3labs adapter.
- **v0.2**: adapter for `modelcontextprotocol/go-sdk`; `Conformance` helper that drives [`@modelcontextprotocol/conformance`](https://github.com/modelcontextprotocol/conformance) from `go test`.
- **v0.3**: fuzz harnesses for JSON-RPC framing + tool inputs; snapshot helpers with `-update` flag.

## Versioning

`mcpharness` follows [SemVer](https://semver.org). Until 1.0, any minor version bump may include breaking API changes (we'll keep them minimal and well-documented in the [CHANGELOG](CHANGELOG.md)).

## Contributing

Issues and PRs welcome. Please open an issue first for any non-trivial change so we can align on direction before you spend time on a PR.

## License

[MIT](LICENSE) © 2026 ultramcu
