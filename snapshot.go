package mcpharness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// UpdateSnapshotsEnv is the environment variable that, when set to a
// truthy value ("1", "true", "yes"), tells [Snapshot] to (re)write
// the on-disk snapshot file instead of comparing. Use it when you've
// intentionally changed behaviour and want to regenerate baselines:
//
//	MCPHARNESS_UPDATE_SNAPSHOTS=1 go test ./...
const UpdateSnapshotsEnv = "MCPHARNESS_UPDATE_SNAPSHOTS"

// SnapshotDir is the on-disk location for snapshot files, relative to
// the test's working directory (which Go conventionally sets to the
// package directory). Override per-call via [WithDir].
const SnapshotDir = "testdata/snapshots"

// SnapshotOption configures [Snapshot].
type SnapshotOption func(*snapshotConfig)

type snapshotConfig struct {
	dir   string
	ext   string
	force bool // forces "update" regardless of env var; mostly for tests
}

// WithDir overrides the directory snapshots live in (default: testdata/snapshots).
func WithDir(dir string) SnapshotOption { return func(c *snapshotConfig) { c.dir = dir } }

// WithExt overrides the on-disk file extension (default: ".json").
func WithExt(ext string) SnapshotOption { return func(c *snapshotConfig) { c.ext = ext } }

// withForceUpdate is an internal option used by snapshot tests to
// force update behaviour without setting a global env var.
func withForceUpdate(force bool) SnapshotOption {
	return func(c *snapshotConfig) { c.force = force }
}

// Snapshot compares got against a stored snapshot file named after
// `name`. Behaviour:
//
//   - On first run (file missing): the canonical JSON form of got is
//     written to disk and the test is logged-but-not-failed.
//   - On subsequent runs: got is canonicalised the same way and
//     compared byte-for-byte to the stored file; on divergence,
//     [Snapshot] calls t.Fatalf with a line-aware diff.
//   - When [UpdateSnapshotsEnv] is set, the file is (re)written and
//     the test is logged-but-not-failed regardless of any divergence.
//
// Snapshots are canonicalised by `json.MarshalIndent` with stable
// (lexicographic) map key order, so unrelated changes in input map
// ordering do not produce false diffs.
//
// The directory `testdata/snapshots/` is created on demand. Snapshot
// files are intended to be committed to the repo as test fixtures.
func Snapshot(t TestingT, name string, got any, opt ...SnapshotOption) {
	t.Helper()
	cfg := snapshotConfig{dir: SnapshotDir, ext: ".json"}
	for _, fn := range opt {
		fn(&cfg)
	}

	safe := sanitizeFilename(name)
	if safe == "" {
		t.Fatalf("mcpharness.Snapshot: name %q sanitises to empty", name)
		return
	}
	path := filepath.Join(cfg.dir, safe+cfg.ext)

	canon, err := canonicalize(got)
	if err != nil {
		t.Fatalf("mcpharness.Snapshot: canonicalize %s: %v", name, err)
		return
	}

	if cfg.force || envTruthy(UpdateSnapshotsEnv) {
		if err := writeSnapshot(path, canon); err != nil {
			t.Fatalf("mcpharness.Snapshot: update %s: %v", path, err)
			return
		}
		t.Logf("mcpharness.Snapshot: updated %s (%d bytes)", path, len(canon))
		return
	}

	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := writeSnapshot(path, canon); err != nil {
			t.Fatalf("mcpharness.Snapshot: write initial %s: %v", path, err)
			return
		}
		t.Logf("mcpharness.Snapshot: created %s (%d bytes) — commit this file and re-run to lock the baseline", path, len(canon))
		return
	}
	if err != nil {
		t.Fatalf("mcpharness.Snapshot: read %s: %v", path, err)
		return
	}

	if bytes.Equal(existing, canon) {
		return
	}
	t.Fatalf("mcpharness.Snapshot: %s differs from %s\n%s\n(re-run with %s=1 to accept the new output as the baseline)",
		name, path, simpleDiff(existing, canon), UpdateSnapshotsEnv)
}

// canonicalize produces a deterministic JSON encoding suitable for
// byte-level comparison. json.MarshalIndent sorts map keys, which is
// what we want for snapshot stability.
func canonicalize(v any) ([]byte, error) {
	// MarshalIndent + a trailing newline so the file ends cleanly and
	// plays nice with editors/git.
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// writeSnapshot creates parent dirs as needed and writes the file.
func writeSnapshot(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, data, 0o644)
}

// sanitizeFilename strips path separators and other characters that
// don't belong in a filename, replacing them with `-`. Multiple
// runs of `-` collapse into one. Leading/trailing `-` trimmed.
func sanitizeFilename(name string) string {
	cleaned := badFilenameChars.ReplaceAllString(name, "-")
	cleaned = collapseDashes.ReplaceAllString(cleaned, "-")
	return strings.Trim(cleaned, "-")
}

var (
	badFilenameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	collapseDashes   = regexp.MustCompile(`-{2,}`)
)

// envTruthy returns true for "1", "true", "yes" (case-insensitive).
func envTruthy(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes"
}

// simpleDiff produces a compact line-diff suitable for inclusion in a
// t.Fatalf message. Not a full unified diff — just enough to point
// the reader at the first mismatch so they can open the file and the
// got-value side-by-side.
func simpleDiff(want, got []byte) string {
	wantLines := strings.Split(string(want), "\n")
	gotLines := strings.Split(string(got), "\n")
	var b strings.Builder
	n := len(wantLines)
	if len(gotLines) > n {
		n = len(gotLines)
	}
	mismatchShown := 0
	const maxMismatch = 5
	for i := 0; i < n && mismatchShown < maxMismatch; i++ {
		w := ""
		if i < len(wantLines) {
			w = wantLines[i]
		}
		g := ""
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w == g {
			continue
		}
		fmt.Fprintf(&b, "  line %d:\n    -want: %s\n    +got:  %s\n", i+1, w, g)
		mismatchShown++
	}
	if mismatchShown == 0 {
		// All visible lines matched; difference must be in trailing
		// whitespace or line count.
		fmt.Fprintf(&b, "  (no per-line diff; want=%d bytes, got=%d bytes — likely trailing whitespace or extra line)\n", len(want), len(got))
	}
	return b.String()
}
