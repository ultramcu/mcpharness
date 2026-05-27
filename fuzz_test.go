package mcpharness_test

import (
	"context"
	"testing"
	"time"

	"github.com/ultramcu/mcpharness"
)

// fuzzableClient is a fake Client that lets a test set the CallTool
// behaviour: succeed, return tool-execution error (IsError=true),
// transport-error, or hang past the harness timeout.
type fuzzableClient struct {
	callBehaviour string // "ok" | "isError" | "transportErr" | "hang"
	callDelay     time.Duration
	calls         int
}

func (c *fuzzableClient) Initialize(ctx context.Context) (*mcpharness.InitResult, error) {
	return &mcpharness.InitResult{ServerName: "fuzz"}, nil
}
func (c *fuzzableClient) ListTools(ctx context.Context) ([]mcpharness.Tool, error) {
	return nil, nil
}
func (c *fuzzableClient) ListResources(ctx context.Context) ([]mcpharness.Resource, error) {
	return nil, nil
}
func (c *fuzzableClient) ReadResource(ctx context.Context, uri string) (*mcpharness.ResourceContents, error) {
	return nil, nil
}
func (c *fuzzableClient) Close() error { return nil }
func (c *fuzzableClient) CallTool(ctx context.Context, name string, args map[string]any) (*mcpharness.CallToolResult, error) {
	c.calls++
	if c.callDelay > 0 {
		select {
		case <-time.After(c.callDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	switch c.callBehaviour {
	case "isError":
		return &mcpharness.CallToolResult{IsError: true}, nil
	case "transportErr":
		return nil, context.Canceled
	default:
		return &mcpharness.CallToolResult{Content: []any{}}, nil
	}
}

// FuzzCallTool_HappyPath is a real fuzz target. `go test ./...`
// (without -fuzz) replays the seed corpus as a regression test, so
// this exercises FuzzCallTool's seed-marshal + JSON-decode + happy-
// path branches every CI run.
//
// To actually fuzz: `go test -fuzz=FuzzCallTool_HappyPath -fuzztime=10s ./...`
func FuzzCallTool_HappyPath(f *testing.F) {
	client := &fuzzableClient{callBehaviour: "ok"}
	mcpharness.FuzzCallTool(f, client, "echo",
		map[string]any{"text": "hello"},
		map[string]any{},
	)
}

// FuzzCallTool_IsErrorOK verifies that a tool reporting IsError=true
// does NOT fail the harness — that's the spec-correct way to return
// handled errors, distinct from a transport panic.
func FuzzCallTool_IsErrorOK(f *testing.F) {
	client := &fuzzableClient{callBehaviour: "isError"}
	mcpharness.FuzzCallTool(f, client, "echo",
		map[string]any{"text": "bad input"},
	)
}
