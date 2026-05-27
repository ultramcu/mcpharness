package mcpharness

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// FuzzCallTool wires a Client + a tool name into Go's native fuzz
// infrastructure. Given one or more seed argument maps, it marshals
// each to JSON, registers the bytes as fuzz seeds, then on each fuzz
// iteration unmarshals the mutated bytes back into a map and invokes
// the tool with a hard per-call timeout.
//
// The test FAILS only on conditions a well-behaved server should
// never produce on adversarial input:
//
//   - The transport returns an error (treated as a protocol panic).
//   - The call hangs past the per-call timeout.
//   - The Go runtime panics inside the handler.
//
// A tool returning `IsError = true` is acceptable — that's the spec-
// correct way to signal a handled tool execution error, and is
// exactly what well-formed validation should produce on garbage input.
//
// Inputs that don't decode as a JSON object are silently skipped (via
// t.SkipNow inside the fuzz callback), so the corpus stays focused on
// "valid JSON, possibly hostile content" — which is the realistic
// threat model for an MCP tool fed by a possibly-misbehaving LLM.
//
// Typical usage:
//
//	func FuzzEchoTool(f *testing.F) {
//	    srv := buildEchoServer()
//	    client, _ := mark3.New(srv)
//	    defer client.Close()
//	    mcpharness.FuzzCallTool(f, client, "echo",
//	        map[string]any{"text": "hello"},
//	        map[string]any{"text": ""},
//	        map[string]any{},
//	    )
//	}
//
// Run with: `go test -fuzz=FuzzEchoTool -fuzztime=30s ./...`
func FuzzCallTool(f *testing.F, client Client, toolName string, seeds ...map[string]any) {
	f.Helper()
	if client == nil {
		f.Fatalf("mcpharness.FuzzCallTool: nil client")
	}
	if toolName == "" {
		f.Fatalf("mcpharness.FuzzCallTool: empty toolName")
	}

	// Always include a few defensible defaults so the fuzz corpus has
	// something to mutate even when the caller forgets to pass seeds.
	if len(seeds) == 0 {
		seeds = []map[string]any{{}, {"_seed": "empty"}}
	}
	for _, s := range seeds {
		b, err := json.Marshal(s)
		if err != nil {
			f.Fatalf("mcpharness.FuzzCallTool: marshal seed %v: %v", s, err)
		}
		f.Add(b)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		t.Helper()
		var args map[string]any
		if err := json.Unmarshal(raw, &args); err != nil {
			// Garbage bytes aren't a valid CallTool argument shape;
			// the harness skips them rather than driving the server
			// with invalid JSON-RPC params. Fuzzing the JSON-RPC
			// framing layer itself is a separate target.
			t.SkipNow()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), defaultFuzzTimeout)
		defer cancel()
		_, err := client.CallTool(ctx, toolName, args)
		if err == nil {
			return
		}
		// Distinguish "the tool itself reported an error" (fine) from
		// transport/timeout failures (not fine for a well-behaved
		// server on adversarial input).
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("mcpharness.FuzzCallTool: %s hung past %s on input %s",
				toolName, defaultFuzzTimeout, string(raw))
		}
		t.Fatalf("mcpharness.FuzzCallTool: %s transport error on input %s: %v",
			toolName, string(raw), err)
	})
}

// defaultFuzzTimeout caps each fuzz iteration. Long enough that a
// well-behaved handler under `-race` overhead never trips it; short
// enough that a real hang shows up as a fuzz failure inside a typical
// `-fuzztime=30s` run instead of blocking the whole campaign.
const defaultFuzzTimeout = 30 * time.Second
