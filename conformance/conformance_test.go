package conformance

import (
	"strings"
	"testing"
)

// captureT is a TestingT that records the first Fatalf / Skipf
// instead of terminating the goroutine. Used to assert Run's
// behaviour without aborting the outer test.
type captureT struct {
	fataled  bool
	skipped  bool
	logged   []string
	msg      string
}

func (c *captureT) Helper() {}
func (c *captureT) Fatalf(format string, args ...any) {
	if c.fataled || c.skipped {
		return
	}
	c.fataled = true
	c.msg = format // good enough for assertions; we use Contains
}
func (c *captureT) Skipf(format string, args ...any) {
	if c.fataled || c.skipped {
		return
	}
	c.skipped = true
	c.msg = format
}
func (c *captureT) Logf(format string, args ...any) {
	c.logged = append(c.logged, format)
}

func TestRun_SkipsWhenNpxMissing(t *testing.T) {
	cap := &captureT{}
	Run(cap, "http://127.0.0.1:0", WithNpxPath("__definitely_not_a_real_binary__"))
	if !cap.skipped {
		t.Errorf("expected Skipf when npx absent, got fataled=%v skipped=%v msg=%q",
			cap.fataled, cap.skipped, cap.msg)
	}
	if !strings.Contains(cap.msg, "npx not on PATH") {
		t.Errorf("Skipf msg = %q, want to mention 'npx not on PATH'", cap.msg)
	}
}

func TestParseHarnessOutput_SummaryShape(t *testing.T) {
	in := []byte(`{"total":10,"passed":8,"failed":2,"skipped":0,"failures":[{"scenario":"core/initialize","message":"protocol version mismatch"},{"scenario":"tools/list","message":"timeout"}]}`)
	got := parseHarnessOutput(in)
	if got.Total != 10 || got.Passed != 8 || got.Failed != 2 {
		t.Fatalf("counts wrong: %+v", got)
	}
	if len(got.Failures) != 2 {
		t.Fatalf("failures len = %d, want 2", len(got.Failures))
	}
	if got.Failures[0].Scenario != "core/initialize" {
		t.Errorf("Failures[0].Scenario = %q", got.Failures[0].Scenario)
	}
	if got.Failures[1].Message != "timeout" {
		t.Errorf("Failures[1].Message = %q", got.Failures[1].Message)
	}
}

func TestParseHarnessOutput_NDJSONShape(t *testing.T) {
	in := []byte(`
{"scenario":"core/initialize","status":"pass"}
{"scenario":"core/list-tools","status":"pass"}
{"scenario":"core/call-tool","status":"fail","message":"missing isError"}
{"scenario":"extensions/sampling","status":"skip"}
`)
	got := parseHarnessOutput(in)
	if got.Total != 4 {
		t.Errorf("Total = %d, want 4", got.Total)
	}
	if got.Passed != 2 {
		t.Errorf("Passed = %d, want 2", got.Passed)
	}
	if got.Failed != 1 {
		t.Errorf("Failed = %d, want 1", got.Failed)
	}
	if got.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", got.Skipped)
	}
	if len(got.Failures) != 1 || got.Failures[0].Message != "missing isError" {
		t.Errorf("Failures = %+v", got.Failures)
	}
}

func TestParseHarnessOutput_Empty(t *testing.T) {
	got := parseHarnessOutput(nil)
	if got.Total != 0 || got.Failed != 0 {
		t.Errorf("empty input should yield zero result, got %+v", got)
	}
	got = parseHarnessOutput([]byte("   \n  "))
	if got.Total != 0 {
		t.Errorf("whitespace input should yield zero result, got %+v", got)
	}
}
