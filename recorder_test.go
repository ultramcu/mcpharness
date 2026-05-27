package mcpharness_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ultramcu/mcpharness"
)

// fakeClient is a hand-written Client used to test Recorder + Replay in
// isolation from any real MCP server framework.
type fakeClient struct {
	initRes         *mcpharness.InitResult
	initErr         error
	tools           []mcpharness.Tool
	callRes         *mcpharness.CallToolResult
	callErr         error
	resourceList    []mcpharness.Resource
	resourceBody    *mcpharness.ResourceContents
	resourceBodyErr error
	closed          bool
}

func (f *fakeClient) Initialize(ctx context.Context) (*mcpharness.InitResult, error) {
	return f.initRes, f.initErr
}
func (f *fakeClient) ListTools(ctx context.Context) ([]mcpharness.Tool, error) {
	return f.tools, nil
}
func (f *fakeClient) CallTool(ctx context.Context, name string, args map[string]any) (*mcpharness.CallToolResult, error) {
	return f.callRes, f.callErr
}
func (f *fakeClient) ListResources(ctx context.Context) ([]mcpharness.Resource, error) {
	return f.resourceList, nil
}
func (f *fakeClient) ReadResource(ctx context.Context, uri string) (*mcpharness.ResourceContents, error) {
	return f.resourceBody, f.resourceBodyErr
}
func (f *fakeClient) Close() error { f.closed = true; return nil }

func TestRecorder_RoundTrip(t *testing.T) {
	fake := &fakeClient{
		initRes: &mcpharness.InitResult{
			ServerName:    "demo",
			ServerVersion: "1.2.3",
			Capabilities:  map[string]any{"tools": map[string]any{}},
		},
		tools: []mcpharness.Tool{
			{Name: "echo", Description: "echoes input", InputSchema: map[string]any{"type": "object"}},
		},
		callRes: &mcpharness.CallToolResult{
			Content: []any{map[string]any{"type": "text", "text": "pong"}},
		},
	}

	var buf bytes.Buffer
	rec := mcpharness.NewRecorder(fake, &buf)
	ctx := context.Background()

	if _, err := rec.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := rec.ListTools(ctx); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if _, err := rec.CallTool(ctx, "echo", map[string]any{"text": "ping"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	rec.Close()
	if !fake.closed {
		t.Errorf("inner client not closed")
	}

	// The recorded file should have exactly 3 lines, one per call.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d recorded lines, want 3:\n%s", len(lines), buf.String())
	}
	for i, want := range []string{`"method":"initialize"`, `"method":"tools/list"`, `"method":"tools/call"`} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d %q does not contain %q", i, lines[i], want)
		}
	}
	if !strings.Contains(lines[2], `"name":"echo"`) {
		t.Errorf("tools/call line missing params: %s", lines[2])
	}
}

func TestReplay_HappyPath(t *testing.T) {
	stream := `{"seq":1,"method":"initialize","result":{"ServerName":"demo","ServerVersion":"1.2.3"}}
{"seq":2,"method":"tools/list","result":[{"Name":"echo","Description":"d","InputSchema":{"type":"object"}}]}
{"seq":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"ping"}},"result":{"Content":[{"type":"text","text":"pong"}],"IsError":false}}
`
	r := strings.NewReader(stream)
	replay := mcpharness.NewReplay(t, r)
	defer replay.Close()
	ctx := context.Background()

	init, err := replay.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.ServerName != "demo" {
		t.Errorf("ServerName = %q, want demo", init.ServerName)
	}

	tools, err := replay.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("tools = %+v, want one echo", tools)
	}

	res, err := replay.CallTool(ctx, "echo", map[string]any{"text": "ping"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) != 1 {
		t.Errorf("res.Content len = %d, want 1", len(res.Content))
	}
}

// TestReplay_MethodMismatch verifies that calling the wrong method
// fails the test loudly via t.Fatalf (proving the divergence-detection
// contract holds).
func TestReplay_MethodMismatch(t *testing.T) {
	stream := `{"seq":1,"method":"initialize"}`

	// captureT intercepts Fatalf instead of failing this test.
	cap := &captureT{}
	replay := mcpharness.NewReplay(cap, strings.NewReader(stream))
	_, _ = replay.ListTools(context.Background()) // wrong method
	if !cap.fataled {
		t.Fatalf("expected Fatalf on method mismatch")
	}
	if !strings.Contains(cap.msg, "expected method") {
		t.Errorf("Fatalf msg = %q, want to mention 'expected method'", cap.msg)
	}
}

// TestReplay_ExhaustedDetectsExtraCall verifies that calling past the
// end of the recorded stream is reported.
func TestReplay_ExhaustedDetectsExtraCall(t *testing.T) {
	cap := &captureT{}
	replay := mcpharness.NewReplay(cap, strings.NewReader(""))
	_, _ = replay.Initialize(context.Background())
	if !cap.fataled || !strings.Contains(cap.msg, "exhausted") {
		t.Errorf("expected 'exhausted' Fatalf, got fataled=%v msg=%q", cap.fataled, cap.msg)
	}
}

// TestReplay_CloseDetectsUnusedEntries verifies that not consuming all
// recorded calls is also a failure (the "test forgot half its
// assertions" class of bug).
func TestReplay_CloseDetectsUnusedEntries(t *testing.T) {
	stream := `{"seq":1,"method":"initialize"}
{"seq":2,"method":"tools/list"}
`
	cap := &captureT{}
	replay := mcpharness.NewReplay(cap, strings.NewReader(stream))
	_, _ = replay.Initialize(context.Background())
	replay.Close()
	if !cap.fataled || !strings.Contains(cap.msg, "unused entries") {
		t.Errorf("expected 'unused entries' Fatalf, got fataled=%v msg=%q", cap.fataled, cap.msg)
	}
}

func TestReplay_PropagatesRecordedError(t *testing.T) {
	stream := `{"seq":1,"method":"initialize","error":"server boom"}`
	replay := mcpharness.NewReplay(t, strings.NewReader(stream))
	defer replay.Close()
	_, err := replay.Initialize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "server boom") {
		t.Errorf("err = %v, want to contain 'server boom'", err)
	}
}

// captureT is a TestingT that records the first Fatalf instead of
// terminating the goroutine. Used to assert Replay's failure messages.
type captureT struct {
	fataled bool
	msg     string
}

func (c *captureT) Helper() {}
func (c *captureT) Fatalf(format string, args ...any) {
	if c.fataled {
		return
	}
	c.fataled = true
	c.msg = fmt.Sprintf(format, args...)
}
