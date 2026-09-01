package player

import (
	"sync"

	"github.com/go-xrkit/depth3d"
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

// convertedSource presents a flat source as a side-by-side one.
//
// It does NOT close the source it wraps: the caller opened that and is already
// closing it, and a second Close on a decoder is not a harmless thing.
type convertedSource struct {
	inner  source
	conv   depth3d.Converter
	info   SourceInfo
	stride int

	mu   sync.Mutex
	free [][]uint32
}

func convertSource(inner source, conv depth3d.Converter) *convertedSource {
	info := inner.Info()
	c := &convertedSource{inner: inner, conv: conv, info: info, stride: info.Width * 2}
	c.info.Width = info.Width * 2
	return c
}

func (c *convertedSource) Info() SourceInfo { return c.info }

// Close releases the converter. The wrapped source is the caller's.
func (c *convertedSource) Close() error {
	c.conv.Close()
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
	// The two eyes are the two halves of one frame, addressed by its own
	// stride -- which is exactly the shape depth3d.Convert takes, so nothing
	// is copied to put them side by side.
	if err := c.conv.Convert(buf, buf[f.Width:], c.stride, f.Pix, f.StrideWords, f.Width, f.Height); err != nil {
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
