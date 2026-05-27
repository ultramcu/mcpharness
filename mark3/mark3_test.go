package mark3_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ultramcu/mcpharness"
	"github.com/ultramcu/mcpharness/mark3"
)

// newEchoServer builds a tiny mark3labs server with one tool ("echo")
// and one resource ("file:///hello.txt") for end-to-end testing.
func newEchoServer(t *testing.T) *server.MCPServer {
	t.Helper()
	srv := server.NewMCPServer("test-echo", "0.1.0")
	srv.AddTool(
		mcp.NewTool("echo",
			mcp.WithDescription("echoes its `text` argument back as content"),
			mcp.WithString("text", mcp.Required()),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			text, _ := req.Params.Arguments.(map[string]any)["text"].(string)
			return mcp.NewToolResultText(text), nil
		},
	)
	srv.AddResource(
		mcp.NewResource("file:///hello.txt", "hello",
			mcp.WithMIMEType("text/plain"),
			mcp.WithResourceDescription("greeting"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      req.Params.URI,
					MIMEType: "text/plain",
					Text:     "hello, world",
				},
			}, nil
		},
	)
	return srv
}

func TestAdapter_EndToEnd(t *testing.T) {
	ctx := context.Background()
	srv := newEchoServer(t)

	client, err := mark3.New(srv)
	if err != nil {
		t.Fatalf("mark3.New: %v", err)
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
	if tools[0].InputSchema["type"] != "object" {
		t.Errorf("InputSchema type = %v, want 'object'", tools[0].InputSchema["type"])
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

// TestAdapter_RecordAndReplay exercises Recorder + Replay together
// using the mark3 adapter as the underlying real server. This is the
// pattern users will actually use: record a real run, commit the file,
// replay deterministically in CI.
func TestAdapter_RecordAndReplay(t *testing.T) {
	ctx := context.Background()
	srv := newEchoServer(t)

	real, err := mark3.New(srv)
	if err != nil {
		t.Fatalf("mark3.New: %v", err)
	}

	var buf bytes.Buffer
	rec := mcpharness.NewRecorder(real, &buf)

	// Drive the recorder through the same sequence we want to replay.
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

	// Now replay against a fresh ReplayClient. Same sequence must
	// produce the same results without touching the real server.
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
	if len(res.Content) != 1 {
		t.Fatalf("replay Content length = %d, want 1", len(res.Content))
	}
}
