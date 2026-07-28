package adb

import (
	"context"
	"errors"
	"testing"
)

// A cancelled context must surface as a context error, not as whatever generic
// failure the killed process happens to produce. Without this, a timeout looks
// to the user like "signal: killed", which points at the wrong problem.
//
// This does not require adb to be installed: run reports the context error
// ahead of any exec failure.
func TestRunReportsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := run(ctx, "devices")
	if err == nil {
		t.Fatal("expected an error from a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
}

func TestRunDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	_, err := run(ctx, "devices")
	if err == nil {
		t.Fatal("expected an error from an expired deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

func TestHumanMB(t *testing.T) {
	tests := []struct {
		mb   int64
		want string
	}{
		{0, "0 MB"},
		{512, "512 MB"},
		{1023, "1023 MB"},
		{1024, "1.0 GB"},
		{1536, "1.5 GB"}, // the reason GB carries a decimal: not "2 GB"
		{10240, "10.0 GB"},
	}
	for _, tt := range tests {
		if got := HumanMB(tt.mb); got != tt.want {
			t.Errorf("HumanMB(%d) = %q, want %q", tt.mb, got, tt.want)
		}
	}
}

// dumpsys emits one very long line per array; jsonArray has to find the right
// label and decode only that line.
func TestJSONArray(t *testing.T) {
	const out = `Package Names: ["com.a","com.b"]
App Sizes: [100,200]
App Data Sizes: [1,2]
`
	names, err := jsonArray[string](out, "Package Names:")
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if len(names) != 2 || names[0] != "com.a" || names[1] != "com.b" {
		t.Errorf("names = %v", names)
	}

	sizes, err := jsonArray[int64](out, "App Sizes:")
	if err != nil {
		t.Fatalf("sizes: %v", err)
	}
	if len(sizes) != 2 || sizes[0] != 100 || sizes[1] != 200 {
		t.Errorf("sizes = %v", sizes)
	}
}

func TestJSONArrayMissingLabel(t *testing.T) {
	if _, err := jsonArray[string]("nothing here\n", "Package Names:"); err == nil {
		t.Error("expected an error when the label is absent")
	}
}

func TestJSONArrayMalformed(t *testing.T) {
	if _, err := jsonArray[int64]("App Sizes: [not json\n", "App Sizes:"); err == nil {
		t.Error("expected an error on malformed JSON")
	}
}

// resolve-activity prefixes the component with metadata lines on some Android
// versions, so the parser takes the last matching line rather than the first.
func TestLastComponent(t *testing.T) {
	const out = `priority=0 preferredOrder=0
  com.example.app/.MainActivity
`
	if got := lastComponent(out, "com.example.app"); got != "com.example.app/.MainActivity" {
		t.Errorf("lastComponent = %q", got)
	}
	if got := lastComponent(out, "com.other.app"); got != "" {
		t.Errorf("lastComponent for an absent package = %q, want empty", got)
	}
}
