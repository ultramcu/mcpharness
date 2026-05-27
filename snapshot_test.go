package mcpharness_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultramcu/mcpharness"
)

// TestSnapshot_CreatesOnFirstRun verifies the first-run path: file
// doesn't exist, snapshot is written, test passes with a log line.
func TestSnapshot_CreatesOnFirstRun(t *testing.T) {
	tmp := t.TempDir()
	got := map[string]any{"name": "echo", "count": 3}
	cap := &captureT{}
	mcpharness.Snapshot(cap, "first-run", got, mcpharness.WithDir(tmp))
	if cap.fataled {
		t.Fatalf("first-run should not fail, got msg=%q", cap.msg)
	}
	wantPath := filepath.Join(tmp, "first-run.json")
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("snapshot file not written: %v", err)
	}
	if !strings.Contains(string(body), `"echo"`) {
		t.Errorf("snapshot body missing content:\n%s", string(body))
	}
}

// TestSnapshot_MatchesExisting verifies the happy path: file exists,
// got matches, test passes silently.
func TestSnapshot_MatchesExisting(t *testing.T) {
	tmp := t.TempDir()
	got := map[string]any{"name": "echo", "count": 3}
	// Seed the file via first-run.
	cap1 := &captureT{}
	mcpharness.Snapshot(cap1, "matches", got, mcpharness.WithDir(tmp))
	if cap1.fataled {
		t.Fatalf("seed write should not fail: %s", cap1.msg)
	}
	// Same got → should match silently.
	cap2 := &captureT{}
	mcpharness.Snapshot(cap2, "matches", got, mcpharness.WithDir(tmp))
	if cap2.fataled {
		t.Fatalf("matching snapshot should not fail: %s", cap2.msg)
	}
}

// TestSnapshot_FailsOnDivergence verifies that a changed value
// produces a Fatalf with a diff hint.
func TestSnapshot_FailsOnDivergence(t *testing.T) {
	tmp := t.TempDir()
	// Seed file with initial value.
	cap1 := &captureT{}
	mcpharness.Snapshot(cap1, "diverges", map[string]any{"v": 1}, mcpharness.WithDir(tmp))
	if cap1.fataled {
		t.Fatalf("seed write failed: %s", cap1.msg)
	}
	// Different value → should Fatalf.
	cap2 := &captureT{}
	mcpharness.Snapshot(cap2, "diverges", map[string]any{"v": 2}, mcpharness.WithDir(tmp))
	if !cap2.fataled {
		t.Fatalf("expected Fatalf on divergence, got pass")
	}
	if !strings.Contains(cap2.msg, "differs from") {
		t.Errorf("Fatalf msg should mention 'differs from', got %q", cap2.msg)
	}
	if !strings.Contains(cap2.msg, mcpharness.UpdateSnapshotsEnv) {
		t.Errorf("Fatalf msg should mention %q for self-recovery, got %q",
			mcpharness.UpdateSnapshotsEnv, cap2.msg)
	}
}

// TestSnapshot_EnvForceUpdate verifies that setting the env var
// rewrites the file instead of failing.
func TestSnapshot_EnvForceUpdate(t *testing.T) {
	tmp := t.TempDir()
	// Seed.
	cap1 := &captureT{}
	mcpharness.Snapshot(cap1, "force", map[string]any{"v": 1}, mcpharness.WithDir(tmp))
	if cap1.fataled {
		t.Fatalf("seed failed: %s", cap1.msg)
	}
	// Set env and re-run with different value — should overwrite, not fail.
	t.Setenv(mcpharness.UpdateSnapshotsEnv, "1")
	cap2 := &captureT{}
	mcpharness.Snapshot(cap2, "force", map[string]any{"v": 99}, mcpharness.WithDir(tmp))
	if cap2.fataled {
		t.Fatalf("env force should not fail, got: %s", cap2.msg)
	}
	body, _ := os.ReadFile(filepath.Join(tmp, "force.json"))
	if !strings.Contains(string(body), "99") {
		t.Errorf("file should contain updated value 99, got:\n%s", string(body))
	}
}

// TestSnapshot_SanitisesUnsafeNames verifies that path-separator and
// other shell-hostile characters in `name` get replaced rather than
// allowing arbitrary file writes. We test the practical safety
// property — the file lands inside the configured dir — rather than
// the cosmetic absence of `..` substrings (which is fine inside a
// hyphen-separated single segment).
func TestSnapshot_SanitisesUnsafeNames(t *testing.T) {
	tmp := t.TempDir()
	cap := &captureT{}
	// Path traversal attempt + spaces + colon — all hostile.
	mcpharness.Snapshot(cap, "../../etc/passwd: attempt", map[string]any{"safe": true},
		mcpharness.WithDir(tmp))
	if cap.fataled {
		t.Fatalf("unexpected fail: %s", cap.msg)
	}
	// 1) Exactly one file landed in tmp (not above it, not sibling).
	entries, _ := os.ReadDir(tmp)
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in tmp, got %d", len(entries))
	}
	name := entries[0].Name()
	// 2) Name must not contain path separators or whitespace — those
	//    are what could enable escape or shell-quoting confusion.
	if strings.ContainsAny(name, "/\\ :\t\n") {
		t.Errorf("file name contains unsafe characters: %q", name)
	}
	// 3) Verify the absolute path resolves inside tmp (not /etc/...).
	abs, err := filepath.Abs(filepath.Join(tmp, name))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if !strings.HasPrefix(abs, tmp) {
		t.Errorf("resolved path %q escaped tmp dir %q", abs, tmp)
	}
}

// TestSnapshot_StableKeyOrdering verifies that map key order doesn't
// affect the canonicalised file (a common snapshot footgun).
func TestSnapshot_StableKeyOrdering(t *testing.T) {
	tmp := t.TempDir()
	// Seed with one ordering.
	cap1 := &captureT{}
	mcpharness.Snapshot(cap1, "ordering",
		map[string]any{"a": 1, "b": 2, "c": 3},
		mcpharness.WithDir(tmp))
	if cap1.fataled {
		t.Fatalf("seed failed: %s", cap1.msg)
	}
	// Same map keys, ostensibly different insertion order — must match.
	cap2 := &captureT{}
	mcpharness.Snapshot(cap2, "ordering",
		map[string]any{"c": 3, "a": 1, "b": 2},
		mcpharness.WithDir(tmp))
	if cap2.fataled {
		t.Fatalf("stable ordering should match across runs, got: %s", cap2.msg)
	}
}
