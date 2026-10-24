package develop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Timings collects per-stage durations of one preview request; handlers emit
// them as a Server-Timing header. A nil *Timings is a no-op.

const timingFloor = 50 * time.Microsecond

type Timings struct {
	mu    sync.Mutex
	start time.Time
	names []string
	durs  map[string]time.Duration
}

type timingsKey struct{}

func WithTimings(ctx context.Context) (context.Context, *Timings) {
	t := &Timings{start: time.Now(), durs: map[string]time.Duration{}}
	return context.WithValue(ctx, timingsKey{}, t), t
}

func timingsFrom(ctx context.Context) *Timings {
	t, _ := ctx.Value(timingsKey{}).(*Timings)
	return t
}

func (t *Timings) Add(name string, d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.durs[name]; !ok {
		t.names = append(t.names, name)
	}
	t.durs[name] += d
}

func (t *Timings) Since(name string, start time.Time) {
	t.Add(name, time.Since(start))
}

func (t *Timings) Total() time.Duration {
	if t == nil {
		return 0
	}
	return time.Since(t.start)
}

func (t *Timings) Header() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	parts := make([]string, 0, len(t.names)+1)
	for _, n := range t.names {
		if d := t.durs[n]; d >= timingFloor {
			parts = append(parts, fmt.Sprintf("%s;dur=%.1f", n, float64(d.Microseconds())/1000))
		}
	}
	parts = append(parts, fmt.Sprintf("total;dur=%.1f", float64(time.Since(t.start).Microseconds())/1000))
	return strings.Join(parts, ", ")
}

func (t *Timings) String() string {
	return strings.ReplaceAll(t.Header(), ";dur=", "=")
}
