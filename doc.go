// Package mcpharness is a testing toolkit for Go MCP server authors.
//
// The Model Context Protocol (MCP) ecosystem has two competing Go server
// frameworks — mark3labs/mcp-go and modelcontextprotocol/go-sdk — plus a
// growing set of domain-specific MCP servers (github, grafana, k8s,
// terraform, …). Every project ends up hand-rolling roughly the same
// test plumbing: a client to drive the server in-process, a way to
// record real sessions for regression tests, an assertion harness for
// tool behaviour.
//
// mcpharness fills that gap with a small SDK-neutral surface:
//
//   - [Client] is the test interface every adapter implements. Two
//     adapters ship: [github.com/ultramcu/mcpharness/mark3] for
//     mark3labs/mcp-go, and [github.com/ultramcu/mcpharness/sdk] for
//     the official modelcontextprotocol/go-sdk.
//
//   - [Recorder] wraps any [Client] and writes every call's request and
//     response to a JSON Lines stream. [Replay] reads such a stream back
//     and returns a deterministic [Client] that asserts each call matches
//     the recorded sequence.
//
// Quick example using the mark3labs adapter:
//
//	import (
//	    "context"
//	    "testing"
//
//	    "github.com/mark3labs/mcp-go/server"
//	    "github.com/ultramcu/mcpharness"
//	    "github.com/ultramcu/mcpharness/mark3"
//	)
//
//	func TestEcho(t *testing.T) {
//	    srv := server.NewMCPServer("echo", "0.1.0")
//	    // ... register tools on srv ...
//
//	    client, err := mark3.New(srv)
//	    if err != nil { t.Fatal(err) }
//	    defer client.Close()
//
//	    if _, err := client.Initialize(context.Background()); err != nil {
//	        t.Fatal(err)
//	    }
//	    tools, _ := client.ListTools(context.Background())
//	    if len(tools) == 0 {
//	        t.Fatal("no tools advertised")
//	    }
//	}
//
// See the mark3 and sdk subpackages for adapter end-to-end patterns,
// and the conformance subpackage for driving Anthropic's official
// test harness from go test.
package mcpharness
