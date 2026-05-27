package mcpharness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Recorder wraps a Client and writes every method call's request and
// response to an io.Writer in JSON Lines format. One line per call.
//
// The recorded stream is a deterministic regression artifact: capture
// a real server session once, commit the file (testdata/<name>.jsonl),
// then drive future tests with Replay against the file. If the server
// changes its behaviour, the test fails with a precise diff.
//
// Recorder is safe for concurrent use only when the wrapped Client is.
type Recorder struct {
	inner Client
	out   io.Writer
	mu    sync.Mutex
	seq   int
}

// NewRecorder wraps inner and writes JSON Lines records to out.
// Typical usage in a test:
//
//	f, _ := os.Create("testdata/echo.jsonl")
//	defer f.Close()
//	client := mcpharness.NewRecorder(realClient, f)
//	// ... drive client as usual ...
func NewRecorder(inner Client, out io.Writer) *Recorder {
	return &Recorder{inner: inner, out: out}
}

// recordedEntry is the on-disk shape. Exported field tags are stable
// across versions — adding new optional fields is fine; renaming is a
// breaking change for replay files.
type recordedEntry struct {
	Seq    int             `json:"seq"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (r *Recorder) write(method string, params, result any, callErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	entry := recordedEntry{Seq: r.seq, Method: method}
	if params != nil {
		entry.Params, _ = json.Marshal(params)
	}
	if callErr != nil {
		entry.Error = callErr.Error()
	} else if result != nil {
		entry.Result, _ = json.Marshal(result)
	}
	line, _ := json.Marshal(entry)
	// One JSON object per line — keeps git diffs readable and lets
	// tools like jq stream-process the file.
	fmt.Fprintln(r.out, string(line))
}

// Initialize records the call.
func (r *Recorder) Initialize(ctx context.Context) (*InitResult, error) {
	res, err := r.inner.Initialize(ctx)
	r.write("initialize", nil, res, err)
	return res, err
}

// ListTools records the call.
func (r *Recorder) ListTools(ctx context.Context) ([]Tool, error) {
	res, err := r.inner.ListTools(ctx)
	r.write("tools/list", nil, res, err)
	return res, err
}

// CallTool records the call.
func (r *Recorder) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	params := map[string]any{"name": name, "arguments": args}
	res, err := r.inner.CallTool(ctx, name, args)
	r.write("tools/call", params, res, err)
	return res, err
}

// ListResources records the call.
func (r *Recorder) ListResources(ctx context.Context) ([]Resource, error) {
	res, err := r.inner.ListResources(ctx)
	r.write("resources/list", nil, res, err)
	return res, err
}

// ReadResource records the call.
func (r *Recorder) ReadResource(ctx context.Context, uri string) (*ResourceContents, error) {
	params := map[string]any{"uri": uri}
	res, err := r.inner.ReadResource(ctx, uri)
	r.write("resources/read", params, res, err)
	return res, err
}

// Close releases the underlying client. The recording writer is the
// caller's responsibility (we don't own it — the caller passed it in).
func (r *Recorder) Close() error {
	return r.inner.Close()
}

// --- Replay ---------------------------------------------------------

// TestingT is the subset of testing.TB that Replay needs. Pass *testing.T
// in your test; the indirection lets the package stay free of a
// testing-only import path in production code.
type TestingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// ReplayClient is a Client that returns deterministic responses from a
// previously-recorded JSON Lines stream. Each call asserts the method
// and params match the next recorded entry; if they don't, the test
// fails via t.Fatalf with a precise diff so the divergence is obvious.
//
// Replay is single-goroutine: it advances a position counter and is
// not safe for concurrent use. Recordings from concurrent runs are
// inherently non-deterministic and should not be replayed.
type ReplayClient struct {
	entries []recordedEntry
	pos     int
	t       TestingT
	closed  bool
}

// NewReplay reads a JSON Lines stream from r and returns a ReplayClient
// that walks it on each call. The Client implementation reports any
// divergence (wrong method, wrong params, extra call, missing call)
// via t.Fatalf.
func NewReplay(t TestingT, r io.Reader) *ReplayClient {
	t.Helper()
	var entries []recordedEntry
	sc := bufio.NewScanner(r)
	// MCP replies (especially tools/call with large content) can blow
	// past bufio.Scanner's default 64 KiB line limit. 1 MiB is enough
	// for any reasonable test response.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e recordedEntry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("mcpharness: replay decode line %d: %v", len(entries)+1, err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("mcpharness: replay read: %v", err)
	}
	return &ReplayClient{entries: entries, t: t}
}

func (c *ReplayClient) next(expectedMethod string, gotParams any) recordedEntry {
	c.t.Helper()
	if c.pos >= len(c.entries) {
		c.t.Fatalf("mcpharness: replay exhausted — extra call to %q (recorded %d entries)", expectedMethod, len(c.entries))
		return recordedEntry{}
	}
	e := c.entries[c.pos]
	c.pos++
	if e.Method != expectedMethod {
		c.t.Fatalf("mcpharness: replay seq=%d expected method %q, got %q", e.Seq, e.Method, expectedMethod)
		return recordedEntry{}
	}
	if gotParams != nil && len(e.Params) > 0 {
		got, _ := json.Marshal(gotParams)
		if !jsonEqual(e.Params, got) {
			c.t.Fatalf("mcpharness: replay seq=%d %s params diverge\n  recorded: %s\n  got:      %s",
				e.Seq, e.Method, string(e.Params), string(got))
		}
	}
	return e
}

func jsonEqual(a, b []byte) bool {
	// Normalize via decode+re-encode so key order / whitespace doesn't
	// produce false negatives.
	var ax, bx any
	if json.Unmarshal(a, &ax) != nil || json.Unmarshal(b, &bx) != nil {
		return false
	}
	an, _ := json.Marshal(ax)
	bn, _ := json.Marshal(bx)
	return string(an) == string(bn)
}

func (c *ReplayClient) Initialize(ctx context.Context) (*InitResult, error) {
	e := c.next("initialize", nil)
	if e.Error != "" {
		return nil, fmt.Errorf("%s", e.Error)
	}
	var r InitResult
	if len(e.Result) > 0 {
		_ = json.Unmarshal(e.Result, &r)
	}
	return &r, nil
}

func (c *ReplayClient) ListTools(ctx context.Context) ([]Tool, error) {
	e := c.next("tools/list", nil)
	if e.Error != "" {
		return nil, fmt.Errorf("%s", e.Error)
	}
	var r []Tool
	if len(e.Result) > 0 {
		_ = json.Unmarshal(e.Result, &r)
	}
	return r, nil
}

func (c *ReplayClient) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	e := c.next("tools/call", map[string]any{"name": name, "arguments": args})
	if e.Error != "" {
		return nil, fmt.Errorf("%s", e.Error)
	}
	var r CallToolResult
	if len(e.Result) > 0 {
		_ = json.Unmarshal(e.Result, &r)
	}
	return &r, nil
}

func (c *ReplayClient) ListResources(ctx context.Context) ([]Resource, error) {
	e := c.next("resources/list", nil)
	if e.Error != "" {
		return nil, fmt.Errorf("%s", e.Error)
	}
	var r []Resource
	if len(e.Result) > 0 {
		_ = json.Unmarshal(e.Result, &r)
	}
	return r, nil
}

func (c *ReplayClient) ReadResource(ctx context.Context, uri string) (*ResourceContents, error) {
	e := c.next("resources/read", map[string]any{"uri": uri})
	if e.Error != "" {
		return nil, fmt.Errorf("%s", e.Error)
	}
	var r ResourceContents
	if len(e.Result) > 0 {
		_ = json.Unmarshal(e.Result, &r)
	}
	return &r, nil
}

// Close marks the replay as finished and fails the test if any
// recorded entries went unused (missing calls). This catches the
// "test didn't drive everything we recorded" class of regression.
func (c *ReplayClient) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	if c.pos < len(c.entries) {
		c.t.Helper()
		c.t.Fatalf("mcpharness: replay closed with %d unused entries (consumed %d of %d)",
			len(c.entries)-c.pos, c.pos, len(c.entries))
	}
	return nil
}
