//go:build darwin

package player

import (
	"testing"
	"time"

	"github.com/go-avkit/avkit/container"
	"github.com/go-macos/videotoolbox"
)

func TestCodecOf(t *testing.T) {
	for _, tc := range []struct {
		fourcc string
		want   videotoolbox.Codec
		ok     bool
	}{
		{"avc1", videotoolbox.H264, true},
		{"avc3", videotoolbox.H264, true},
		{"hvc1", videotoolbox.HEVC, true},
		{"hev1", videotoolbox.HEVC, true},
		{"av01", 0, false},
		{"vp09", 0, false},
		{"mp4a", 0, false},
		{"", 0, false},
	} {
		got, ok := codecOf(tc.fourcc)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("codecOf(%q) = (%v,%v), want (%v,%v)", tc.fourcc, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTicks(t *testing.T) {
	for _, tc := range []struct {
		n         int64
		timescale uint32
		want      time.Duration
	}{
		{0, 1000, 0},
		{1000, 1000, time.Second},
		{600, 600, time.Second},
		{512, 15360, time.Second / 30},
		{-1000, 1000, -time.Second},
	} {
		got := ticks(tc.n, tc.timescale)
		if d := got - tc.want; d > time.Nanosecond || d < -time.Nanosecond {
			t.Errorf("ticks(%d, %d) = %v, want %v", tc.n, tc.timescale, got, tc.want)
		}
	}
}

// TestTimedSamples pins the timeline arithmetic. A demuxer states each sample's
// DURATION, not its position: decoding time is the running sum, and presentation
// time is that plus the composition offset a reordered stream carries. Getting
// this wrong is not visible as a wrong picture, only as a wrong pace — which is
// far harder to notice and to attribute.
func TestTimedSamples(t *testing.T) {
	const ts = 1000
	raw := []container.Sample{
		{Data: []byte{1}, Duration: 100},
		{Data: []byte{2}, Duration: 100},
		{Data: []byte{3}, Duration: 100},
	}
	got := timedSamples(raw, ts)
	if len(got) != 3 {
		t.Fatalf("got %d samples, want 3", len(got))
	}
	for i, want := range []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond} {
		if got[i].PTS != want {
			t.Errorf("sample %d PTS = %v, want %v", i, got[i].PTS, want)
		}
		if got[i].Duration != 100*time.Millisecond {
			t.Errorf("sample %d duration = %v, want 100ms", i, got[i].Duration)
		}
		if len(got[i].Data) != 1 || got[i].Data[0] != byte(i+1) {
			t.Errorf("sample %d carries %v, want the demuxer's bytes", i, got[i].Data)
		}
	}

	// A composition offset shifts presentation without moving decoding: the
	// second frame is DECODED second but SHOWN third.
	reordered := []container.Sample{
		{Duration: 100, CompositionOffset: 0},
		{Duration: 100, CompositionOffset: 200},
		{Duration: 100, CompositionOffset: -100},
	}
	rg := timedSamples(reordered, ts)
	for i, want := range []time.Duration{0, 300 * time.Millisecond, 100 * time.Millisecond} {
		if rg[i].PTS != want {
			t.Errorf("reordered sample %d PTS = %v, want %v", i, rg[i].PTS, want)
		}
	}

	// A zero timescale would divide by zero; it falls back rather than
	// producing infinities that travel a long way before failing.
	z := timedSamples([]container.Sample{{Duration: 1000}, {Duration: 1000}}, 0)
	if z[1].PTS != time.Second {
		t.Errorf("with a zero timescale the second PTS = %v, want 1s under the 1000 fallback", z[1].PTS)
	}
	if got := timedSamples(nil, ts); len(got) != 0 {
		t.Errorf("timedSamples(nil) returned %d samples", len(got))
	}
}

// TestPopEarliest is the reorder buffer's whole job. VideoToolbox emits frames
// in DECODING order, so a stream with B-frames hands them over out of
// presentation order; showing them as they arrive plays the picture with a
// stutter that reads as a decode fault and is not one.
func TestPopEarliest(t *testing.T) {
	mk := func(ms ...int) []*videotoolbox.Frame {
		out := make([]*videotoolbox.Frame, len(ms))
		for i, m := range ms {
			out[i] = &videotoolbox.Frame{PTS: time.Duration(m) * time.Millisecond}
		}
		return out
	}

	// The classic IPBB pattern: decoded 0, 3, 1, 2 — shown 0, 1, 2, 3.
	pending := mk(0, 300, 100, 200)
	var order []int
	for len(pending) > 0 {
		var f *videotoolbox.Frame
		f, pending = popEarliest(pending)
		order = append(order, int(f.PTS/time.Millisecond))
	}
	want := []int{0, 100, 200, 300}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("emitted %v, want %v", order, want)
		}
	}

	// An empty buffer yields nothing and does not panic.
	if f, rest := popEarliest(nil); f != nil || len(rest) != 0 {
		t.Errorf("popEarliest(nil) = (%v, %v)", f, rest)
	}
	// A single frame comes straight back.
	one := mk(42)
	f, rest := popEarliest(one)
	if f == nil || f.PTS != 42*time.Millisecond || len(rest) != 0 {
		t.Errorf("popEarliest of one frame = (%v, %d left)", f, len(rest))
	}
	// Equal timestamps must not lose a frame.
	eq := mk(5, 5, 5)
	n := 0
	for len(eq) > 0 {
		_, eq = popEarliest(eq)
		n++
	}
	if n != 3 {
		t.Errorf("three equal timestamps emitted %d frames", n)
	}
}
