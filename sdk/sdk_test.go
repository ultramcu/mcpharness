package sdk_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ultramcu/mcpharness"
	"github.com/ultramcu/mcpharness/sdk"
)

// newEchoServer builds a tiny go-sdk MCP server with one tool ("echo")
// and one resource ("file:///hello.txt") for end-to-end testing.
func newEchoServer(t *testing.T) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "test-echo",
		Version: "0.1.0",
	}, nil)

	// go-sdk has TWO AddTool surfaces: the untyped method
	// (s.AddTool(t, h)) and the generic package-level helper
	// (mcp.AddTool[In,Out](s, t, h)) which auto-validates against the
	// declared schema. We use the generic form here to exercise both
	// the typed input path and the auto-schema validation.
	type echoArgs struct {
		Text string `json:"text"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "echo",
		Description: "echoes its `text` argument back as content",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []any{"text"},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: args.Text}},
		}, nil, nil
	})

	srv.AddResource(&mcp.Resource{
		URI:         "file:///hello.txt",
		Name:        "hello",
		Description: "greeting",
		MIMEType:    "text/plain",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "text/plain",
					Text:     "hello, world",
				},
			},
		}, nil
	})
	return srv
}

func TestAdapter_EndToEnd(t *testing.T) {
	ctx := context.Background()
	srv := newEchoServer(t)

	client, err := sdk.New(srv)
	if err != nil {
		t.Fatalf("sdk.New: %v", err)
	}
	defer client.Close()

	init, err := client.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.ServerName != "test-echo" {
		t.Errorf("ServerName = %q, want %q", init.ServerName, "test-echo")
	}
	if init.ServerVersion != "0.1.0" {
		t.Errorf("ServerVersion = %q, want %q", init.ServerVersion, "0.1.0")
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("ListTools = %+v, want one tool named 'echo'", tools)
	}
	if !strings.Contains(tools[0].Description, "echoes") {
		t.Errorf("tool Description = %q, missing 'echoes'", tools[0].Description)
	}

	res, err := client.CallTool(ctx, "echo", map[string]any{"text": "ping"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("IsError = true, want false")
	}
	if len(res.Content) != 1 {
		t.Fatalf("Content length = %d, want 1", len(res.Content))
	}
	got, _ := res.Content[0].(map[string]any)
	if got["type"] != "text" || got["text"] != "ping" {
		t.Errorf("Content[0] = %+v, want type=text text=ping", got)
	}

	resources, err := client.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 1 || resources[0].URI != "file:///hello.txt" {
		t.Fatalf("ListResources = %+v, want one 'file:///hello.txt'", resources)
	}
	if resources[0].MimeType != "text/plain" {
		t.Errorf("MimeType = %q, want text/plain", resources[0].MimeType)
	}

	body, err := client.ReadResource(ctx, "file:///hello.txt")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if body.Text != "hello, world" {
		t.Errorf("Text = %q, want 'hello, world'", body.Text)
	}
}

// TestAdapter_RecordAndReplay verifies the SDK adapter composes
// cleanly with the base-package Recorder + Replay.
func TestAdapter_RecordAndReplay(t *testing.T) {
	ctx := context.Background()
	srv := newEchoServer(t)

	real, err := sdk.New(srv)
	if err != nil {
		t.Fatalf("sdk.New: %v", err)
	}

	var buf bytes.Buffer
	rec := mcpharness.NewRecorder(real, &buf)

	if _, err := rec.Initialize(ctx); err != nil {
		t.Fatalf("rec.Initialize: %v", err)
	}
	if _, err := rec.ListTools(ctx); err != nil {
		t.Fatalf("rec.ListTools: %v", err)
	}
	if _, err := rec.CallTool(ctx, "echo", map[string]any{"text": "ping"}); err != nil {
		t.Fatalf("rec.CallTool: %v", err)
	}
	rec.Close()

	replay := mcpharness.NewReplay(t, &buf)
	defer replay.Close()

	init, err := replay.Initialize(ctx)
	if err != nil {
		t.Fatalf("replay Initialize: %v", err)
	}
	if init.ServerName != "test-echo" {
		t.Errorf("replay ServerName = %q, want test-echo", init.ServerName)
	}

	tools, err := replay.ListTools(ctx)
	if err != nil {
		t.Fatalf("replay ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("replay tools = %+v, want one echo", tools)
	}

	res, err := replay.CallTool(ctx, "echo", map[string]any{"text": "ping"})
	if err != nil {
		t.Fatalf("replay CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("replay IsError = true, want false")
	}
}
