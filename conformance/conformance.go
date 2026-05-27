// Package conformance bridges Anthropic's official conformance test
// harness (https://github.com/modelcontextprotocol/conformance) to Go
// tests.
//
// The official conformance suite is a TypeScript / npx tool that
// connects to a running MCP server over HTTP and exercises every
// scenario defined in the MCP spec (core handshake, capability
// negotiation, tools/resources/prompts, auth extensions, etc.). None
// of the production Go MCP servers we surveyed run it in CI — there
// was no easy way to drive it from go test.
//
// This package provides that bridge: [Run] takes a URL pointing at
// your MCP server's HTTP transport and asserts via t.Fatalf on any
// conformance failure. Skips automatically if npx is not on PATH.
//
// Usage:
//
//	func TestMCPConformance(t *testing.T) {
//	    srv := startMyServerOnRandomPort(t)
//	    conformance.Run(t, srv.URL)
//	}
//
// To narrow the scenario set (faster iteration during development),
// pass [WithSuite]:
//
//	conformance.Run(t, srv.URL, conformance.WithSuite("core"))
//
// Requires Node.js / npx. The official package is fetched lazily by
// npx on first run (cached under ~/.npm afterwards).
package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Option configures [Run].
type Option func(*config)

type config struct {
	suite     string
	skip      []string
	timeout   time.Duration
	npxPath   string
	pkgRef    string
	extraArgs []string
}

// WithSuite limits the run to a single suite name (e.g. "core",
// "extensions", "auth"). Default: "all".
func WithSuite(name string) Option { return func(c *config) { c.suite = name } }

// WithSkip excludes named scenarios from the run. Useful when a
// known-broken behaviour is being tracked separately. Repeatable.
func WithSkip(scenario ...string) Option { return func(c *config) { c.skip = append(c.skip, scenario...) } }

// WithTimeout caps the total wall-clock time the harness is allowed
// to run. Default: 2 minutes.
func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }

// WithNpxPath overrides the discovered npx binary. Useful in
// hermetic CI where npx lives at a known absolute path.
func WithNpxPath(p string) Option { return func(c *config) { c.npxPath = p } }

// WithPackage overrides the npm package npx runs. Defaults to the
// official "@modelcontextprotocol/conformance". Pin a specific version
// like "@modelcontextprotocol/conformance@0.5.0" to keep CI reproducible.
func WithPackage(pkg string) Option { return func(c *config) { c.pkgRef = pkg } }

// WithExtraArgs appends raw flags to the harness command, after the
// flags this package sets. Use for one-off flags the typed options
// don't cover yet.
func WithExtraArgs(args ...string) Option {
	return func(c *config) { c.extraArgs = append(c.extraArgs, args...) }
}

// TestingT is the subset of testing.TB this package needs. Pass
// *testing.T in your test.
type TestingT interface {
	Helper()
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
	Skipf(format string, args ...any)
}

// Result summarises a conformance run. Returned by Run for callers
// who want to introspect counts; the test has already been failed via
// t.Fatalf for any non-zero Failed before Result is returned, unless
// the caller used a fault-tolerant TestingT.
type Result struct {
	Total    int
	Passed   int
	Failed   int
	Skipped  int
	Failures []Failure
	Raw      string // captured stdout (for diagnostic logging)
}

// Failure describes a single failed conformance scenario.
type Failure struct {
	Scenario string
	Message  string
}

// Run drives the official conformance harness against the server at
// url and fails t if any scenario fails. Skips the test (via t.Skipf)
// when npx is not on PATH.
//
// The harness is invoked roughly as:
//
//	npx --yes @modelcontextprotocol/conformance server --url <url> --json
//
// stdout is parsed as JSON for the structured result; stderr is
// captured and logged via t.Logf on failure for diagnosis.
func Run(t TestingT, url string, opt ...Option) Result {
	t.Helper()
	cfg := config{
		suite:   "all",
		timeout: 2 * time.Minute,
		npxPath: "npx",
		pkgRef:  "@modelcontextprotocol/conformance",
	}
	for _, fn := range opt {
		fn(&cfg)
	}

	npx, err := exec.LookPath(cfg.npxPath)
	if err != nil {
		t.Skipf("mcpharness/conformance: npx not on PATH (%v) — skipping conformance run; install Node.js to enable", err)
		return Result{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	args := []string{"--yes", cfg.pkgRef, "server", "--url", url, "--json"}
	if cfg.suite != "all" && cfg.suite != "" {
		args = append(args, "--suite", cfg.suite)
	}
	for _, s := range cfg.skip {
		args = append(args, "--skip", s)
	}
	args = append(args, cfg.extraArgs...)

	cmd := exec.CommandContext(ctx, npx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	// We tolerate a non-zero exit code: the harness returns non-zero
	// when scenarios fail, which is exactly what we're here to detect.
	res := parseHarnessOutput(stdout.Bytes())
	res.Raw = stdout.String()

	// If we couldn't parse anything AND the command errored, surface
	// the underlying execution problem rather than a confusing "0
	// scenarios" report.
	if res.Total == 0 && runErr != nil {
		t.Logf("mcpharness/conformance: stderr:\n%s", stderr.String())
		t.Fatalf("mcpharness/conformance: harness execution failed: %v", runErr)
		return res
	}

	if res.Failed > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "mcpharness/conformance: %d/%d scenarios failed:\n", res.Failed, res.Total)
		for _, f := range res.Failures {
			fmt.Fprintf(&b, "  - %s: %s\n", f.Scenario, f.Message)
		}
		t.Logf("mcpharness/conformance: stderr:\n%s", stderr.String())
		t.Fatalf("%s", b.String())
	}

	return res
}

// parseHarnessOutput accepts either:
//   - a top-level object with {total, passed, failed, skipped, failures: [...]}
//   - or an array of per-scenario records {scenario, status, message}
//
// We're conservative because the harness output format has evolved
// across versions; the parser tolerates both shapes and ignores
// unknown fields.
func parseHarnessOutput(stdout []byte) Result {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return Result{}
	}

	// Shape 1: summary object.
	var summary struct {
		Total    int `json:"total"`
		Passed   int `json:"passed"`
		Failed   int `json:"failed"`
		Skipped  int `json:"skipped"`
		Failures []struct {
			Scenario string `json:"scenario"`
			Name     string `json:"name"`
			Message  string `json:"message"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(trimmed, &summary); err == nil && (summary.Total > 0 || len(summary.Failures) > 0) {
		out := Result{
			Total:   summary.Total,
			Passed:  summary.Passed,
			Failed:  summary.Failed,
			Skipped: summary.Skipped,
		}
		for _, f := range summary.Failures {
			name := f.Scenario
			if name == "" {
				name = f.Name
			}
			out.Failures = append(out.Failures, Failure{Scenario: name, Message: f.Message})
		}
		return out
	}

	// Shape 2: per-scenario records (one object per scenario, possibly
	// streamed line-by-line as NDJSON). Treat any non-"pass" status as
	// failure unless explicitly "skip".
	var out Result
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record struct {
			Scenario string `json:"scenario"`
			Name     string `json:"name"`
			Status   string `json:"status"`
			Message  string `json:"message"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		if record.Scenario == "" && record.Name == "" && record.Status == "" {
			continue
		}
		out.Total++
		switch strings.ToLower(record.Status) {
		case "pass", "passed", "ok":
			out.Passed++
		case "skip", "skipped":
			out.Skipped++
		default:
			out.Failed++
			name := record.Scenario
			if name == "" {
				name = record.Name
			}
			out.Failures = append(out.Failures, Failure{Scenario: name, Message: record.Message})
		}
	}
	return out
}

// errBadOutput is returned from internal parse paths when the harness
// produced output we couldn't make sense of. Currently unused publicly
// — kept for tests that may want to assert on it.
var errBadOutput = errors.New("mcpharness/conformance: unparseable harness output")
