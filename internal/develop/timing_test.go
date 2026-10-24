package develop

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTimingsHeader(t *testing.T) {
	ctx, tm := WithTimings(context.Background())
	if timingsFrom(ctx) != tm {
		t.Fatal("timings not stored in context")
	}
	tm.Add("decode", 12*time.Millisecond)
	tm.Add("decode", 3*time.Millisecond)
	tm.Add("noise", time.Microsecond)
	h := tm.Header()
	if !strings.Contains(h, "decode;dur=15.0") {
		t.Errorf("header = %q", h)
	}
	if strings.Contains(h, "noise") {
		t.Errorf("sub-floor entries must be dropped: %q", h)
	}
	if !strings.Contains(h, "total;dur=") {
		t.Errorf("total missing: %q", h)
	}
}

func TestNilTimingsSafe(t *testing.T) {
	var tm *Timings
	tm.Add("x", time.Second)
	tm.Since("y", time.Now())
	if tm.Header() != "" || tm.Total() != 0 {
		t.Error("nil timings must be a no-op")
	}
	if timingsFrom(context.Background()) != nil {
		t.Error("plain context must carry no timings")
	}
}
