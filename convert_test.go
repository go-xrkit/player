package player

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/go-xrkit/depth3d"
)

// fakeSource yields a fixed number of frames whose every pixel says which
// frame and which column it came from.
type fakeSource struct {
	w, h, stride, n int
	given           int
	released        int
	closed          bool
	// opaque makes the frames look like a decoder's. The default does not,
	// because the tests that read exact pixel values are easier to follow
	// without an alpha byte in every expectation.
	opaque bool
}

func (f *fakeSource) Info() SourceInfo {
	return SourceInfo{Width: f.w, Height: f.h, FrameRate: 24}
}

func (f *fakeSource) Close() error { f.closed = true; return nil }

func (f *fakeSource) Next() (*srcFrame, error) {
	if f.given >= f.n {
		return nil, io.EOF
	}
	f.given++
	pix := make([]uint32, f.stride*f.h)
	for y := 0; y < f.h; y++ {
		for x := 0; x < f.w; x++ {
			v := uint32(f.given)<<16 | uint32(x)
			if f.opaque {
				v |= 0xFF000000
			}
			pix[y*f.stride+x] = v
		}
	}
	return &srcFrame{
		Width: f.w, Height: f.h, StrideWords: f.stride,
		PTS: time.Duration(f.given) * time.Second,
		Pix: pix, release: func() { f.released++ },
	}, nil
}

// markerConverter does no image work at all: it writes a value that says which
// half and which column, so the PLUMBING can be checked exactly while the
// image algorithm is tested where it lives.
type markerConverter struct {
	calls  int
	closed bool
	fail   bool
}

func (m *markerConverter) Describe() string { return "a converter for the test" }
func (m *markerConverter) Close()           { m.closed = true }

func (m *markerConverter) Convert(left, right []uint32, stride int, src []uint32, srcStride, w, h int) error {
	m.calls++
	if m.fail {
		return depth3d.ErrNothingToConvert
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			left[y*stride+x] = 0x1000000 | src[y*srcStride+x]
			right[y*stride+x] = 0x2000000 | src[y*srcStride+x]
		}
	}
	return nil
}

func TestAConvertedSourceIsTwiceAsWideAndSaysSo(t *testing.T) {
	// The sampling tables are built from Info before a single frame is warped,
	// so a source that reported the old width would build them for half the
	// picture and show the left eye stretched across both.
	src := &fakeSource{w: 16, h: 4, stride: 20, n: 3}
	c := convertSource(src, &markerConverter{})
	if got := c.Info().Width; got != 32 {
		t.Fatalf("converted width %d, want 32", got)
	}
	if got := c.Info().Height; got != 4 {
		t.Fatalf("converted height %d, want 4", got)
	}
	if got := c.Info().FrameRate; got != 24 {
		t.Errorf("the frame rate was lost: %v", got)
	}
}

func TestEachConvertedFrameCarriesBothEyesAndTheOriginalMoment(t *testing.T) {
	src := &fakeSource{w: 16, h: 4, stride: 20, n: 2}
	c := convertSource(src, &markerConverter{})
	f, err := c.Next()
	if err != nil {
		t.Fatal(err)
	}
	if f.Width != 32 || f.Height != 4 || f.StrideWords != 32 {
		t.Fatalf("frame is %dx%d stride %d, want 32x4 stride 32", f.Width, f.Height, f.StrideWords)
	}
	if f.PTS != time.Second {
		t.Errorf("PTS is %v, want the source's own 1s", f.PTS)
	}
	for _, x := range []int{0, 7, 15} {
		if got := f.Pix[2*32+x]; got != 0x1010000|uint32(x) {
			t.Errorf("left half at x=%d is %#x", x, got)
		}
		if got := f.Pix[2*32+16+x]; got != 0x2010000|uint32(x) {
			t.Errorf("right half at x=%d is %#x", x, got)
		}
	}
	// The source's frame must be handed back whatever happens next: the
	// decoder owns a fixed pool, and a leak stalls playback rather than
	// failing it.
	if src.released != 1 {
		t.Errorf("the source frame was released %d times, want 1", src.released)
	}
	f.Release()
}

func TestConvertedFramesAreRecycledRatherThanReallocated(t *testing.T) {
	// A converted 1080p frame is sixteen megabytes. Allocating one per frame
	// is half a gigabyte a second of garbage.
	src := &fakeSource{w: 16, h: 4, stride: 20, n: 4}
	c := convertSource(src, &markerConverter{})
	first, err := c.Next()
	if err != nil {
		t.Fatal(err)
	}
	firstBuf := &first.Pix[0]
	first.Release()
	second, err := c.Next()
	if err != nil {
		t.Fatal(err)
	}
	if &second.Pix[0] != firstBuf {
		t.Error("the released buffer was not reused")
	}
	// And a frame still in hand must NOT be handed out again: the previous
	// frame is on screen while the next one is being composed.
	third, err := c.Next()
	if err != nil {
		t.Fatal(err)
	}
	if &third.Pix[0] == &second.Pix[0] {
		t.Fatal("two live frames were given the same memory")
	}
}

func TestAConvertedSourceReportsTheEndAndItsFailures(t *testing.T) {
	src := &fakeSource{w: 8, h: 2, stride: 8, n: 1}
	c := convertSource(src, &markerConverter{})
	if _, err := c.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("at the end of the file: %v, want io.EOF", err)
	}

	bad := &fakeSource{w: 8, h: 2, stride: 8, n: 1}
	failing := &markerConverter{fail: true}
	cf := convertSource(bad, failing)
	if _, err := cf.Next(); !errors.Is(err, depth3d.ErrNothingToConvert) {
		t.Fatalf("a converter that failed gave %v", err)
	}
	if bad.released != 1 {
		t.Errorf("the source frame was not released after a failure (%d)", bad.released)
	}
}

func TestClosingAConvertedSourceClosesTheConverterAndNotTheSource(t *testing.T) {
	// Play already defers Close on the source it opened. Closing a decoder
	// twice is not a harmless thing, so the wrapper must not do it again.
	src := &fakeSource{w: 8, h: 2, stride: 8, n: 1}
	conv := &markerConverter{}
	c := convertSource(src, conv)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !conv.closed {
		t.Error("the converter was not closed")
	}
	if src.closed {
		t.Error("the wrapper closed the source it does not own")
	}
}

func TestARealConverterFitsTheFramesThisSourceHandsIt(t *testing.T) {
	// The plumbing tests above use a stand-in converter, which accepts
	// anything. The real one checks the sizes it is given -- and the right eye
	// is a SUB-SLICE of the frame, so its length is exactly one eye short of
	// the buffer. An off-by-one there is refused, silently, on every frame.
	// Opaque, unlike the stand-in source above: a real decoder never hands over
	// a transparent pixel, and the synthesis carries alpha through untouched.
	src := &fakeSource{w: 32, h: 8, stride: 40, n: 2, opaque: true}
	conv, err := depth3d.New(depth3d.Options{MaxShift: 12})
	if err != nil {
		t.Fatal(err)
	}
	c := convertSource(src, conv)
	defer c.Close()

	f, err := c.Next()
	if err != nil {
		t.Fatalf("a real converter refused the frames this source produces: %v", err)
	}
	defer f.Release()
	if f.Width != 64 || f.StrideWords != 64 {
		t.Fatalf("frame is %d wide, stride %d", f.Width, f.StrideWords)
	}
	for y := 0; y < f.Height; y++ {
		for x := 0; x < f.Width; x++ {
			if f.Pix[y*f.StrideWords+x]>>24 == 0 {
				t.Fatalf("a transparent pixel at %d,%d; an eye was not written", x, y)
			}
		}
	}
}
