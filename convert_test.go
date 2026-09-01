package player

import (
	"errors"
	"io"
	"testing"
	"time"
)

// fakeSource yields a fixed number of frames whose every pixel says which
// frame and which column it came from.
type fakeSource struct {
	w, h, stride, n int
	given           int
	released        int
	closed          bool
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
			pix[y*f.stride+x] = uint32(f.given)<<16 | uint32(x)
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

func (m *markerConverter) describe() string { return "a converter for the test" }
func (m *markerConverter) close()           { m.closed = true }

func (m *markerConverter) convert(dst []uint32, dstStride int, src []uint32, srcStride, w, h int) error {
	m.calls++
	if m.fail {
		return errNothingToConvert
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst[y*dstStride+x] = 0x1000000 | src[y*srcStride+x]
			dst[y*dstStride+w+x] = 0x2000000 | src[y*srcStride+x]
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
	if _, err := cf.Next(); !errors.Is(err, errNothingToConvert) {
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

func TestTheCueConverterFillsBothEyesCompletely(t *testing.T) {
	// The image algorithm is tested where it lives, in go-images/depth. What
	// matters here is that it is wired up the right way round: both halves
	// written, every pixel opaque, and the eyes not identical.
	const w, h = 64, 32
	stride := w + 3 // a padded row, as a real decoder produces
	src := make([]uint32, stride*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Detail at the bottom and flat sky at the top, so the cues have
			// something to disagree about.
			v := uint32(0xFF000000)
			if y > h/2 && x%3 == 0 {
				v |= 0x00FFFFFF
			}
			src[y*stride+x] = v
		}
	}
	dst := make([]uint32, 2*w*h)
	c := &cueConverter{maxShift: 24, radius: 2}
	if err := c.convert(dst, 2*w, src, stride, w, h); err != nil {
		t.Fatal(err)
	}
	same := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			l, r := dst[y*2*w+x], dst[y*2*w+w+x]
			if l>>24 == 0 || r>>24 == 0 {
				t.Fatalf("a transparent pixel survived at %d,%d", x, y)
			}
			if l == r {
				same++
			}
		}
	}
	if same == w*h {
		t.Fatal("the two eyes are identical; nothing was converted")
	}
}

func TestTheCueConverterRefusesAFrameWithNoPicture(t *testing.T) {
	c := &cueConverter{maxShift: 24, radius: 2}
	if err := c.convert(make([]uint32, 4), 2, make([]uint32, 4), 2, 0, 0); !errors.Is(err, errNothingToConvert) {
		t.Fatalf("a picture of nothing gave %v", err)
	}
	if c.describe() == "" {
		t.Error("the converter does not say what it is")
	}
	c.close()
}

// textured builds a picture in which every COLUMN is distinct, padded like a
// decoder's frame.
//
// Distinct on purpose: a repeating pattern makes a shift land back on itself,
// so a picture that moved a long way scores as having moved less than one that
// barely moved. The first version of this test used one and failed for that
// reason rather than for a fault in the code.
func textured(w, h, stride int) []uint32 {
	src := make([]uint32, stride*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := uint32(x * 2 % 256)
			src[y*stride+x] = 0xFF000000 | c<<16 | c<<8 | c
		}
	}
	return src
}

// moved counts how far the synthesis actually moved the picture: the pixels of
// one eye that no longer match the source underneath them.
func moved(dst []uint32, dstStride int, src []uint32, srcStride, w, h int) int {
	n := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if dst[y*dstStride+x] != src[y*srcStride+x] {
				n++
			}
		}
	}
	return n
}

func TestABiggerDisparityMovesThingsFurther(t *testing.T) {
	// The absolute amount depends on the depth map and is not worth asserting.
	// That MORE disparity moves MORE is the property that says the number
	// reaches the synthesis at all -- and it is the one that failed silently
	// when a snapshot at two settings came out byte for byte identical,
	// because the test frame was nearly black and every depth was the same.
	const w, h = 96, 48
	stride := w + 5
	src := textured(w, h, stride)

	count := func(maxShift int) int {
		dst := make([]uint32, 2*w*h)
		c := &cueConverter{maxShift: maxShift, radius: 2}
		if err := c.convert(dst, 2*w, src, stride, w, h); err != nil {
			t.Fatal(err)
		}
		return moved(dst, 2*w, src, stride, w, h)
	}
	small, large := count(8), count(48)
	if small == 0 {
		t.Fatal("nothing moved at all")
	}
	if large <= small {
		t.Fatalf("a disparity of 48 moved %d pixels, no more than 8 did (%d)", large, small)
	}
	if none := count(0); none != count(24) {
		// Zero means "use the default", not "do not move" -- Options treats it
		// that way and so must this, or a caller who leaves it unset gets a
		// flat picture and no explanation.
		t.Error("a disparity of zero did not fall back to the default")
	}
}

func TestAPictureWithNothingInItComesOutFlatRatherThanWrong(t *testing.T) {
	// A frame with no structure -- a fade to black, which is how a film starts
	// -- gives a depth map with no range at all. There is nothing to move, and
	// the right answer is two identical eyes rather than a guess.
	const w, h = 64, 32
	stride := w + 2
	src := make([]uint32, stride*h)
	for i := range src {
		src[i] = 0xFF000000
	}
	dst := make([]uint32, 2*w*h)
	c := &cueConverter{maxShift: 24, radius: 2}
	if err := c.convert(dst, 2*w, src, stride, w, h); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if dst[y*2*w+x]>>24 == 0 || dst[y*2*w+w+x]>>24 == 0 {
				t.Fatalf("a transparent pixel at %d,%d", x, y)
			}
		}
	}
}
