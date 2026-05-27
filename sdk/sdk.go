// Package sdk is an mcpharness adapter for modelcontextprotocol/go-sdk
// (the official Anthropic Go SDK).
//
// Wrap a *mcp.Server with [New] to drive it in-process from tests using
// the SDK-neutral [mcpharness.Client] interface. The adapter pairs the
// server with an in-memory transport, connects a client, and translates
// between the SDK's typed request/response structs and mcpharness's
// smaller cross-SDK surface.
//
// Importing this package pulls in github.com/modelcontextprotocol/go-sdk
// (which requires Go 1.25 or later). Projects that only need the
// recorder/replay or the mark3 adapter can import other mcpharness
// subpackages alone and pay no extra dependency cost.
package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ultramcu/mcpharness"
)

// Option configures the sdk adapter.
type Option func(*options)

type options struct {
	clientName    string
	clientVersion string
}

// WithClientInfo overrides the name/version reported in the initialize
// handshake. Default: "mcpharness-sdk" / "0.2.0".
func WithClientInfo(name, version string) Option {
	return func(o *options) { o.clientName = name; o.clientVersion = version }
}

// adapter implements mcpharness.Client by wrapping a go-sdk
// ClientSession bound to an in-memory transport.
type adapter struct {
	clientSess *mcp.ClientSession
	serverSess *mcp.ServerSession
	opts       options
	mu         sync.Mutex
	closed     bool
}

// New constructs an mcpharness.Client that talks to srv in-process via
// an in-memory transport. The server's Connect runs on a background
// context detached from the test's context — this keeps the server
// alive across the test's individual call-context cancellations and
// mirrors how production servers are started.
//
// Call Close on the returned client when the test is done to release
// the transport goroutines on both sides.
func New(srv *mcp.Server, opt ...Option) (mcpharness.Client, error) {
	o := options{
		clientName:    "mcpharness-sdk",
		clientVersion: "0.2.0",
	}
	for _, fn := range opt {
		fn(&o)
	}

	clientT, serverT := mcp.NewInMemoryTransports()

	// Server-side: detached background context — survives per-call
	// cancellations from the test side. The session goroutines will
	// shut down when the transport is closed (via adapter.Close).
	serverSess, err := srv.Connect(context.Background(), serverT, nil)
	if err != nil {
		return nil, fmt.Errorf("mcpharness/sdk: server.Connect: %w", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    o.clientName,
		Version: o.clientVersion,
	}, nil)

	// Client-side Connect performs the initialize handshake implicitly.
	clientSess, err := client.Connect(context.Background(), clientT, nil)
	if err != nil {
		_ = serverSess.Close()
		return nil, fmt.Errorf("mcpharness/sdk: client.Connect: %w", err)
	}

	return &adapter{
		clientSess: clientSess,
		serverSess: serverSess,
		opts:       o,
	}, nil
}

func (a *adapter) Initialize(ctx context.Context) (*mcpharness.InitResult, error) {
	// go-sdk performs initialize implicitly in Connect; we return the
	// cached InitializeResult so the mcpharness.Client contract holds.
	res := a.clientSess.InitializeResult()
	if res == nil {
		return nil, mcpharness.ErrNotInitialized
	}
	caps := map[string]any{}
	if b, err := json.Marshal(res.Capabilities); err == nil {
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
	res, err := a.clientSess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, err
	}
	out := make([]mcpharness.Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		schema := map[string]any{}
		if t.InputSchema != nil {
			if b, mErr := json.Marshal(t.InputSchema); mErr == nil {
				_ = json.Unmarshal(b, &schema)
			}
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
	res, err := a.clientSess.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, err
	}
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
	res, err := a.clientSess.ListResources(ctx, &mcp.ListResourcesParams{})
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
	res, err := a.clientSess.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, err
	}
	if len(res.Contents) == 0 {
		return &mcpharness.ResourceContents{URI: uri}, nil
	}
	// As with the mark3 adapter, v0.x returns the first content; a
	// multi-content read API will land in a future release.
	first := res.Contents[0]
	out := &mcpharness.ResourceContents{
		URI:      first.URI,
		MimeType: first.MIMEType,
	}
	// go-sdk's ResourceContents holds both Text (string) and Blob
	// ([]byte); whichever the server populated, we copy through.
	out.Text = first.Text
	out.Blob = first.Blob
	if out.URI == "" {
		out.URI = uri
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
	// Close the client session first so any in-flight server-side
	// requests unwind cleanly; then close the server session.
	cErr := a.clientSess.Close()
	sErr := a.serverSess.Close()
	if cErr != nil {
		return cErr
	}
	return sErr
}
