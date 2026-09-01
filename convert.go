package player

import (
	"errors"
	"image"
	"sync"

	"github.com/go-images/depth"
)

// Turning an ordinary flat film into something the glasses can show in 3D.
//
// The whole of it happens BEFORE the renderer: a converter wraps the source and
// presents side-by-side frames, so the warp, the projection, the control bar
// and the snapshot path go on believing they were handed a 3D film. Nothing
// downstream of here knows this feature exists.
//
// What it cannot do is invent what the camera never saw. Where a near object
// moves aside, the pixels behind it are guessed from the same row -- so an
// edge is a little smeared, and that is the honest cost of the effect.

// errNothingToConvert is what a converter says when the frame it was given
// cannot be turned into a pair -- an empty picture, or a depth map that does
// not match it. It is not a user error and it is not recoverable per frame, so
// playback stops rather than showing something flat and pretending.
var errNothingToConvert = errors.New("player: nothing to convert in this frame")

// converter turns one flat frame into a side-by-side pair.
type converter interface {
	// convert writes 2w by h pixels into dst, left eye then right.
	convert(dst []uint32, dstStride int, src []uint32, srcStride, w, h int) error
	// describe says which path is doing the work and why, for the log. A
	// converter that quietly fell back to the cheap estimate would look
	// identical from the outside except for being worse.
	describe() string
	close()
}

// cueConverter is the portable path: depth guessed from the picture itself,
// and the two views synthesised on the processor. No model, no GPU, no
// download -- it works on any machine, and it is visibly not as good as a real
// depth network.
type cueConverter struct {
	maxShift int
	radius   int
}

func (c *cueConverter) describe() string {
	return "depth from cues in the picture, views on the processor"
}

func (c *cueConverter) close() {}

func (c *cueConverter) convert(dst []uint32, dstStride int, src []uint32, srcStride, w, h int) error {
	// The decoder's frames are BGRA and image.RGBA reads them as RGBA, so red
	// and blue are transposed for the luminance term. It is left that way on
	// purpose: correcting it costs a copy of every frame, and what it changes
	// is the WEIGHTS of a greyscale that only has to say which parts of the
	// picture are bright. The pixels themselves are moved whole, so nothing
	// that reaches the eye is discoloured.
	img := &image.RGBA{
		Pix:    asBytes(src)[:srcStride*h*4],
		Stride: srcStride * 4,
		Rect:   image.Rect(0, 0, w, h),
	}
	m := depth.Soften(depth.Cues(img), c.radius)
	left, right := depth.Views(img, m, depth.Options{MaxShift: c.maxShift})
	if left == nil || right == nil {
		return errNothingToConvert
	}
	packSideBySide(dst, dstStride, left, right, w, h)
	return nil
}

// packSideBySide lays the two eyes out in one frame, left then right.
func packSideBySide(dst []uint32, dstStride int, left, right *image.RGBA, w, h int) {
	lw, rw := asWords(left.Pix), asWords(right.Pix)
	ls, rs := left.Stride/4, right.Stride/4
	for y := 0; y < h; y++ {
		copy(dst[y*dstStride:y*dstStride+w], lw[y*ls:y*ls+w])
		copy(dst[y*dstStride+w:y*dstStride+2*w], rw[y*rs:y*rs+w])
	}
}

// convertedSource presents a flat source as a side-by-side one.
//
// It does NOT close the source it wraps: the caller opened that and is already
// closing it, and a second Close on a decoder is not a harmless thing.
type convertedSource struct {
	inner  source
	conv   converter
	info   SourceInfo
	stride int

	mu   sync.Mutex
	free [][]uint32
}

func convertSource(inner source, conv converter) *convertedSource {
	info := inner.Info()
	c := &convertedSource{inner: inner, conv: conv, info: info, stride: info.Width * 2}
	c.info.Width = info.Width * 2
	return c
}

func (c *convertedSource) Info() SourceInfo { return c.info }

// Close releases the converter. The wrapped source is the caller's.
func (c *convertedSource) Close() error {
	c.conv.close()
	return nil
}

func (c *convertedSource) Next() (*srcFrame, error) {
	f, err := c.inner.Next()
	if err != nil {
		return nil, err
	}
	out, err := c.frame(f)
	f.Release()
	return out, err
}

// frame converts one decoded frame, and is separate from Next so that the
// FIRST frame -- which the caller already pulled, to measure the stride before
// the sampling tables could be built -- goes through exactly the same path.
func (c *convertedSource) frame(f *srcFrame) (*srcFrame, error) {
	buf := c.take(c.stride * f.Height)
	if err := c.conv.convert(buf, c.stride, f.Pix, f.StrideWords, f.Width, f.Height); err != nil {
		c.give(buf)
		return nil, err
	}
	return &srcFrame{
		Width:       f.Width * 2,
		Height:      f.Height,
		StrideWords: c.stride,
		PTS:         f.PTS,
		Pix:         buf,
		release:     func() { c.give(buf) },
	}, nil
}

// take and give recycle the converted frames.
//
// A converted 1080p frame is sixteen megabytes. Allocating one per frame is
// half a gigabyte a second of garbage, and holding a single buffer would hand
// the same memory to two frames whenever one is still on screen -- so they go
// back on a free list when Released, which is what release is for.
func (c *convertedSource) take(n int) []uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, b := range c.free {
		if len(b) >= n {
			c.free = append(c.free[:i], c.free[i+1:]...)
			return b[:n]
		}
	}
	return make([]uint32, n)
}

func (c *convertedSource) give(b []uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.free) < 4 {
		c.free = append(c.free, b)
	}
}
