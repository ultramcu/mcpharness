package mcpharness

import (
	"context"
	"errors"
)

// Client is the SDK-neutral test interface that MCP server tests target.
//
// Adapters in subpackages (mark3, and future sdk) provide concrete
// implementations against the underlying server frameworks. Tests stay
// written against this interface so they survive framework choice and
// minor version drift.
//
// The interface intentionally covers the 80% of calls that tool tests
// actually need (Initialize, list/call tools, list/read resources).
// Prompts, sampling, completion, subscriptions, and logging are deferred
// until a real test demands them — keeping the surface small is the
// whole point.
type Client interface {
	// Initialize performs the MCP initialize handshake and returns the
	// server's advertised name/version/capabilities. Must be called once
	// before any other method.
	Initialize(ctx context.Context) (*InitResult, error)

	// ListTools returns the server's advertised tools after handshake.
	ListTools(ctx context.Context) ([]Tool, error)

	// CallTool invokes a tool by name with structured arguments. The
	// returned result preserves the server's content blocks and the
	// IsError flag (which distinguishes a tool-execution error from a
	// protocol error — the latter comes back via the error return).
	CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error)

	// ListResources returns the server's advertised resources.
	ListResources(ctx context.Context) ([]Resource, error)

	// ReadResource fetches the contents of a single resource by URI.
	ReadResource(ctx context.Context, uri string) (*ResourceContents, error)

	// Close releases the underlying client/transport. Safe to call
	// multiple times; subsequent calls return nil.
	Close() error
}

// InitResult mirrors the subset of MCP InitializeResult that tests care
// about. Capabilities is the raw map from the server — tests can
// type-assert into it for capability-specific checks.
type InitResult struct {
	ServerName      string
	ServerVersion   string
	ProtocolVersion string
	Capabilities    map[string]any
}

// Tool is the SDK-neutral view of a tool entry from `tools/list`.
// InputSchema is the raw JSON schema, kept as a generic map so tests
// can assert on any field without binding to a schema-library type.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// CallToolResult mirrors `tools/call` results. Content is the raw
// content array from the server — each element is typically a map with
// at least a "type" key ("text", "image", "resource"). IsError is the
// MCP-spec flag that signals a tool-execution error (distinct from a
// transport or protocol error, which the caller gets via err).
type CallToolResult struct {
	Content []any
	IsError bool
}

// Resource is the SDK-neutral view of a resource entry from
// `resources/list`.
type Resource struct {
	URI         string
	Name        string
	Description string
	MimeType    string
}

// ResourceContents holds the body of a single resource read. Exactly
// one of Text or Blob is populated based on MimeType — callers can
// switch on `len(Blob) > 0` or check MimeType.
type ResourceContents struct {
	URI      string
	MimeType string
	Text     string
	Blob     []byte
}

// ErrNotInitialized signals a method was called before Initialize.
// Adapters MAY return this directly; the interface contract says
// Initialize must be the first call.
var ErrNotInitialized = errors.New("mcpharness: Initialize not called")
