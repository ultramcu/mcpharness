// Package mark3 is an mcpharness adapter for mark3labs/mcp-go.
//
// Wrap a *server.MCPServer with [New] to drive it in-process from tests
// using the SDK-neutral [mcpharness.Client] interface. The adapter
// translates between mark3labs's typed request/response structs and
// mcpharness's smaller cross-SDK surface.
//
// Importing this package pulls in github.com/mark3labs/mcp-go. Projects
// that only need the recorder/replay can import github.com/ultramcu/mcpharness
// alone and pay no extra dependency cost.
package mark3

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ultramcu/mcpharness"
)

// Option configures a mark3 adapter.
type Option func(*options)

type options struct {
	clientName    string
	clientVersion string
	protoVersion  string
}

// WithClientInfo overrides the name/version reported in the initialize
// handshake. Default: "mcpharness-mark3" / "0.1.0".
func WithClientInfo(name, version string) Option {
	return func(o *options) { o.clientName = name; o.clientVersion = version }
}

// WithProtocolVersion overrides the protocol version requested in the
// initialize handshake. Default: mcp.LATEST_PROTOCOL_VERSION.
func WithProtocolVersion(v string) Option {
	return func(o *options) { o.protoVersion = v }
}

// adapter implements mcpharness.Client by wrapping a mark3labs in-process
// client.
type adapter struct {
	inner *client.Client
	opts  options
	mu    sync.Mutex
	once  sync.Once
	closed bool
}

// New constructs an mcpharness.Client that talks to srv in-process.
// The transport is started immediately. Call Close on the returned
// client when the test is done to release the transport goroutine.
func New(srv *server.MCPServer, opt ...Option) (mcpharness.Client, error) {
	o := options{
		clientName:    "mcpharness-mark3",
		clientVersion: "0.1.0",
		protoVersion:  mcp.LATEST_PROTOCOL_VERSION,
	}
	for _, fn := range opt {
		fn(&o)
	}
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		return nil, fmt.Errorf("mcpharness/mark3: NewInProcessClient: %w", err)
	}
	if err := c.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("mcpharness/mark3: client.Start: %w", err)
	}
	return &adapter{inner: c, opts: o}, nil
}

func (a *adapter) Initialize(ctx context.Context) (*mcpharness.InitResult, error) {
	req := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: a.opts.protoVersion,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    a.opts.clientName,
				Version: a.opts.clientVersion,
			},
		},
	}
	res, err := a.inner.Initialize(ctx, req)
	if err != nil {
		return nil, err
	}
	// ServerCapabilities is a typed struct; marshal+unmarshal into a
	// generic map so tests can introspect without binding to the
	// mark3labs type.
	caps := map[string]any{}
	if b, mErr := json.Marshal(res.Capabilities); mErr == nil {
		_ = json.Unmarshal(b, &caps)
	}
	return &mcpharness.InitResult{
		ServerName:      res.ServerInfo.Name,
		ServerVersion:   res.ServerInfo.Version,
		ProtocolVersion: res.ProtocolVersion,
		Capabilities:    caps,
	}, nil
}

func (a *adapter) ListTools(ctx context.Context) ([]mcpharness.Tool, error) {
	res, err := a.inner.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]mcpharness.Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		schema := map[string]any{}
		if b, mErr := json.Marshal(t.InputSchema); mErr == nil {
			_ = json.Unmarshal(b, &schema)
		}
		out = append(out, mcpharness.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out, nil
}

func (a *adapter) CallTool(ctx context.Context, name string, args map[string]any) (*mcpharness.CallToolResult, error) {
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}
	res, err := a.inner.CallTool(ctx, req)
	if err != nil {
		return nil, err
	}
	// Normalize content into a generic []any. Each element keeps its
	// "type" key so tests can switch on text/image/resource without
	// depending on mark3labs concrete types.
	content := make([]any, 0, len(res.Content))
	for _, c := range res.Content {
		var generic any
		if b, mErr := json.Marshal(c); mErr == nil {
			_ = json.Unmarshal(b, &generic)
		}
		content = append(content, generic)
	}
	return &mcpharness.CallToolResult{
		Content: content,
		IsError: res.IsError,
	}, nil
}

func (a *adapter) ListResources(ctx context.Context) ([]mcpharness.Resource, error) {
	res, err := a.inner.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]mcpharness.Resource, 0, len(res.Resources))
	for _, r := range res.Resources {
		out = append(out, mcpharness.Resource{
			URI:         r.URI,
			Name:        r.Name,
			Description: r.Description,
			MimeType:    r.MIMEType,
		})
	}
	return out, nil
}

func (a *adapter) ReadResource(ctx context.Context, uri string) (*mcpharness.ResourceContents, error) {
	req := mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	}
	res, err := a.inner.ReadResource(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(res.Contents) == 0 {
		return &mcpharness.ResourceContents{URI: uri}, nil
	}
	// MCP spec allows multiple contents per read; mcpharness's v0.1
	// surface returns the first (the common case). Multi-content reads
	// will get a list-returning method in v0.2.
	first := res.Contents[0]
	out := &mcpharness.ResourceContents{URI: uri}
	switch v := first.(type) {
	case mcp.TextResourceContents:
		out.URI = v.URI
		out.MimeType = v.MIMEType
		out.Text = v.Text
	case mcp.BlobResourceContents:
		out.URI = v.URI
		out.MimeType = v.MIMEType
		// mark3labs holds the blob as a base64 string; decode lazily so
		// tests can assert either on the raw base64 (via the recorder)
		// or on the decoded bytes. We expose decoded bytes for
		// programmatic use.
		out.Blob = []byte(v.Blob)
	}
	return out, nil
}

func (a *adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	var err error
	a.once.Do(func() {
		err = a.inner.Close()
	})
	return err
}
