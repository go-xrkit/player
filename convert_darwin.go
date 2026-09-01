package player

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-macos/coreml"
	"github.com/go-macos/metal"
)

// The accelerated path: a real depth network on the Neural Engine, and the two
// views synthesised by compute kernels on the GPU.
//
// Both halves are here for the same reason. On a machine that is also decoding
// video, drawing a window and running a desktop, the processor is the scarce
// thing -- and measured on an M4 Max, a frame of this costs about four tenths
// of a millisecond of it. The same work on sixteen cores costs eighty.
//
// The Neural Engine is asked for deliberately rather than letting Core ML
// choose. It is NOT the fastest of the three processors -- the GPU is, by
// nearly half -- but the GPU here already has the eyes to synthesise and a
// desktop to draw, and 36 frames a second is more than a 24 fps film needs.

const gpuKernels = `
#include <metal_stdlib>
using namespace metal;

struct P { uint w, h, dw, dh, maxShift; };

// The depth map is a different SIZE from the picture: a network has its own
// input size and does not care what it was given.
// The depth map is a different SIZE from the picture -- a network has its own
// input size and does not care what it was given -- so it is INTERPOLATED.
//
// Taking the nearest map pixel manufactures a depth step every time the picture
// crosses a map pixel boundary: 2099 of them across one 1080p frame where the
// map itself holds 28, on a regular grid, answering to nothing in the picture.
// They read as a staircase over every smooth surface.
//
// Integer arithmetic, matching go-images/depth exactly, because the two are
// checked against each other BYTE FOR BYTE. In long, not int: the products
// below reach 8.5e9 at 4K and would overflow 32 bits in silence.
static inline uint depthAt(device const uchar *d, constant P &p, uint x, uint y) {
    int dw = int(p.dw), dh = int(p.dh), w = int(p.w), h = int(p.h);
    int denx = 2 * w, px = (2 * int(x) + 1) * dw - w;
    int ix = px / denx; if (px < 0 && px % denx != 0) ix--;
    int tx = px - ix * denx;
    int deny = 2 * h, py = (2 * int(y) + 1) * dh - h;
    int iy = py / deny; if (py < 0 && py % deny != 0) iy--;
    int ty = py - iy * deny;
    int x0 = max(ix, 0), x1 = min(ix + 1, dw - 1);
    int y0 = max(iy, 0), y1 = min(iy + 1, dh - 1);
    long top = (long)d[y0 * dw + x0] * (denx - tx) + (long)d[y0 * dw + x1] * tx;
    long bot = (long)d[y1 * dw + x0] * (denx - tx) + (long)d[y1 * dw + x1] * tx;
    return uint((top * (deny - ty) + bot * ty + (long)denx * deny / 2) / ((long)denx * deny));
}

// Softening the depth step, separably. Not cosmetic: a step crossing the pixel
// grid makes an edge pixel jump five pixels of disparity between frames, which
// reads as the edge boiling. It is real geometry rather than noise -- a median
// over three frames removes none of it -- so the remedy is spatial. Over the
// network's own small map it costs nothing.
kernel void blurH(device const uchar *in [[buffer(0)]], device uchar *out [[buffer(1)]],
                  constant P &p [[buffer(2)]], constant int &r [[buffer(3)]],
                  uint2 g [[thread_position_in_grid]]) {
    if (g.x >= p.dw || g.y >= p.dh) return;
    uint sum = 0, n = 0;
    for (int k = -r; k <= r; k++) {
        int sx = int(g.x) + k;
        if (sx >= 0 && sx < int(p.dw)) { sum += in[g.y * p.dw + uint(sx)]; n++; }
    }
    out[g.y * p.dw + g.x] = uchar(sum / n);
}

kernel void blurV(device const uchar *in [[buffer(0)]], device uchar *out [[buffer(1)]],
                  constant P &p [[buffer(2)]], constant int &r [[buffer(3)]],
                  uint2 g [[thread_position_in_grid]]) {
    if (g.x >= p.dw || g.y >= p.dh) return;
    uint sum = 0, n = 0;
    for (int k = -r; k <= r; k++) {
        int sy = int(g.y) + k;
        if (sy >= 0 && sy < int(p.dh)) { sum += in[uint(sy) * p.dw + g.x]; n++; }
    }
    out[g.y * p.dw + g.x] = uchar(sum / n);
}

// The map, interpolated to the picture's own size, ONCE.
//
// The synthesis below asks for a depth up to twenty-four times per pixel, and
// paying for the interpolation at each ask cost seventy per cent of the frame.
// Eight megabytes at 4K buys all of it back.
kernel void upsample(device const uchar *small [[buffer(0)]], device uchar *full [[buffer(1)]],
                     constant P &p [[buffer(2)]], uint2 g [[thread_position_in_grid]]) {
    if (g.x >= p.w || g.y >= p.h) return;
    full[g.y * p.w + g.x] = uchar(depthAt(small, p, g.x, g.y));
}

// The two eyes, written straight into one side-by-side frame.
//
// It GATHERS: each output pixel asks which source pixels could have reached it
// and keeps the nearest. That is the same rule as painting far pixels first so
// near ones land on top, without the global sort by depth -- and without it no
// two threads could share a row.
kernel void eyes(device const uchar4 *src [[buffer(0)]],
                 device const uchar *depth [[buffer(1)]],
                 device uchar4 *out [[buffer(2)]],
                 constant P &p [[buffer(3)]],
                 uint2 g [[thread_position_in_grid]]) {
    if (g.x >= p.w || g.y >= p.h) return;
    uint halfShift = p.maxShift / 2;
    uint row = g.y * p.w * 2;

    int best = -1, bestD = -1;
    for (uint k = 0; k <= halfShift; k++) {
        if (k > g.x) break;
        uint sx = g.x - k, d = depth[g.y * p.w + sx];
        if ((d * p.maxShift) / 255 / 2 == k && int(d) >= bestD) { bestD = int(d); best = int(sx); }
    }
    out[row + g.x] = best < 0 ? uchar4(0) : src[g.y * p.w + uint(best)];

    best = -1; bestD = -1;
    for (uint k = 0; k <= halfShift; k++) {
        uint sx = g.x + k;
        if (sx >= p.w) break;
        uint d = depth[g.y * p.w + sx];
        if ((d * p.maxShift) / 255 / 2 == k && int(d) >= bestD) { bestD = int(d); best = int(sx); }
    }
    out[row + p.w + g.x] = best < 0 ? uchar4(0) : src[g.y * p.w + uint(best)];
}

// Where a near object moved aside it revealed something the camera never saw.
// The nearest filled pixel on the same row is stretched into it -- both
// directions, since a hole can open before any filled pixel -- and each eye is
// closed WITHIN ITS OWN HALF, or the right eye would be filled from the left
// eye's last column.
kernel void fill(device uchar4 *img [[buffer(0)]], constant P &p [[buffer(1)]],
                 uint y [[thread_position_in_grid]]) {
    if (y >= p.h) return;
    // Not "half": that is a TYPE in Metal Shading Language -- a sixteen-bit
    // float -- and a variable of that name fails to compile in a way that
    // reads as a syntax error twenty lines long.
    for (uint eye = 0; eye < 2; eye++) {
        uint row = y * p.w * 2 + eye * p.w;
        int last = -1;
        for (uint x = 0; x < p.w; x++) {
            if (img[row+x].a != 0) last = int(x);
            else if (last >= 0) img[row+x] = img[row + uint(last)];
        }
        last = -1;
        for (int x = int(p.w) - 1; x >= 0; x--) {
            if (img[row+uint(x)].a != 0) last = x;
            else if (last >= 0) img[row+uint(x)] = img[row + uint(last)];
        }
    }
}
`

type gpuParams struct{ W, H, DW, DH, MaxShift uint32 }

type gpuConverter struct {
	model    *coreml.Model
	in, out  coreml.Feature
	dev      *metal.Device
	lib      *metal.Library
	eyes     *metal.Pipeline
	fill     *metal.Pipeline
	blurH    *metal.Pipeline
	blurV    *metal.Pipeline
	upsample *metal.Pipeline
	maxShift int
	radius   int32

	w, h                            int
	srcB, depthB, tmpB, fullB, outB *metal.Buffer
	srcM, depthM, outM              []byte
	small                           []byte
	modelName                       string
}

func (g *gpuConverter) describe() string {
	return fmt.Sprintf("depth by %s on the Neural Engine, views on %s",
		filepath.Base(g.modelName), g.dev.Name())
}

func (g *gpuConverter) close() {
	for _, b := range []*metal.Buffer{g.srcB, g.depthB, g.tmpB, g.fullB, g.outB} {
		b.Close()
	}
	g.eyes.Close()
	g.fill.Close()
	g.blurH.Close()
	g.blurV.Close()
	g.upsample.Close()
	g.lib.Close()
	g.dev.Close()
	g.model.Close()
}

// newGPUConverter opens everything the accelerated path needs, and returns an
// error naming which piece was missing rather than falling back in silence.
func newGPUConverter(modelPath string, maxShift, radius int) (*gpuConverter, error) {
	compiled, err := compiledModel(modelPath)
	if err != nil {
		return nil, err
	}
	m, err := coreml.Open(compiled, coreml.CPUAndNeuralEngine)
	if err != nil {
		return nil, err
	}
	in, out := m.Inputs(), m.Outputs()
	if len(in) != 1 || in[0].Kind != coreml.KindImage || in[0].Width < 1 {
		m.Close()
		return nil, fmt.Errorf("player: %s takes %v; this needs a model that takes one image", compiled, in)
	}
	if len(out) < 1 {
		m.Close()
		return nil, fmt.Errorf("player: %s returns nothing", compiled)
	}
	dev, err := metal.Default()
	if err != nil {
		m.Close()
		return nil, err
	}
	lib, err := dev.Compile(gpuKernels)
	if err != nil {
		dev.Close()
		m.Close()
		return nil, err
	}
	g := &gpuConverter{
		model: m, in: in[0], out: out[0], dev: dev, lib: lib,
		maxShift: maxShift, radius: int32(radius), modelName: compiled,
		small: make([]byte, in[0].Width*in[0].Height*4),
	}
	for _, p := range []struct {
		name string
		dst  **metal.Pipeline
	}{{"eyes", &g.eyes}, {"fill", &g.fill}, {"blurH", &g.blurH}, {"blurV", &g.blurV}, {"upsample", &g.upsample}} {
		pipe, err := lib.Pipeline(p.name)
		if err != nil {
			lib.Close()
			dev.Close()
			m.Close()
			return nil, err
		}
		*p.dst = pipe
	}
	return g, nil
}

// compiledModel turns an .mlpackage into the .mlmodelc Core ML actually runs,
// and keeps it in the user's cache directory.
//
// Not beside the model: that may be anywhere, including inside a repository,
// and a fifty-megabyte build artefact one `git add -A` from being committed is
// not something to leave lying about.
func compiledModel(path string) (string, error) {
	if strings.HasSuffix(path, ".mlmodelc") {
		return path, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("player: nowhere to keep the compiled model: %w", err)
	}
	dir := filepath.Join(cache, "xrplay", "models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, strings.TrimSuffix(filepath.Base(path), ".mlpackage")+".mlmodelc")
	return dst, coreml.Compile(path, dst)
}

func (g *gpuConverter) resize(w, h int) error {
	if g.w == w && g.h == h {
		return nil
	}
	for _, b := range []*metal.Buffer{g.srcB, g.depthB, g.tmpB, g.fullB, g.outB} {
		b.Close()
	}
	var err error
	alloc := func(n int) (*metal.Buffer, []byte) {
		if err != nil {
			return nil, nil
		}
		var b *metal.Buffer
		b, err = g.dev.NewBuffer(n)
		if err != nil {
			return nil, nil
		}
		return b, b.Bytes()
	}
	g.srcB, g.srcM = alloc(w * h * 4)
	g.depthB, g.depthM = alloc(g.in.Width * g.in.Height)
	g.tmpB, _ = alloc(g.in.Width * g.in.Height)
	g.fullB, _ = alloc(w * h)
	g.outB, g.outM = alloc(w * h * 4 * 2)
	if err != nil {
		return err
	}
	g.w, g.h = w, h
	return nil
}

func (g *gpuConverter) convert(dst []uint32, dstStride int, src []uint32, srcStride, w, h int) error {
	if w < 1 || h < 1 {
		return errNothingToConvert
	}
	if err := g.resize(w, h); err != nil {
		return err
	}
	srcBytes := asBytes(src)
	for y := 0; y < h; y++ {
		copy(g.srcM[y*w*4:(y+1)*w*4], srcBytes[y*srcStride*4:])
	}
	// Scaled down to what the network was trained on, and left in BGRA, which
	// is both what the decoder produced and what Core Video wants.
	for y := 0; y < g.in.Height; y++ {
		sy := y * h / g.in.Height
		for x := 0; x < g.in.Width; x++ {
			s := (sy*w + x*w/g.in.Width) * 4
			d := (y*g.in.Width + x) * 4
			copy(g.small[d:d+4], g.srcM[s:s+4])
			g.small[d+3] = 255
		}
	}
	res, err := g.model.Predict(map[string]coreml.Value{
		g.in.Name: coreml.Image(g.small, g.in.Width, g.in.Height),
	})
	if err != nil {
		return err
	}
	plane, err := res.Plane(g.out.Name)
	res.Close()
	if err != nil {
		return err
	}
	// The network's scale is relative, so the plane is stretched onto 0..255
	// before it is used to move pixels sideways.
	copy(g.depthM, plane.Normalised())

	p := gpuParams{uint32(w), uint32(h), uint32(g.in.Width), uint32(g.in.Height), uint32(g.maxShift)}
	if err := g.dev.Run(func(e *metal.Encoder) {
		e.Use(g.blurH)
		e.Buffer(0, g.depthB)
		e.Buffer(1, g.tmpB)
		metal.Constant(e, 2, &p)
		metal.Constant(e, 3, &g.radius)
		e.Dispatch(g.in.Width, g.in.Height)

		e.Use(g.blurV)
		e.Buffer(0, g.tmpB)
		e.Buffer(1, g.depthB)
		e.Dispatch(g.in.Width, g.in.Height)

		e.Use(g.upsample)
		e.Buffer(0, g.depthB)
		e.Buffer(1, g.fullB)
		metal.Constant(e, 2, &p)
		e.Dispatch(w, h)

		e.Use(g.eyes)
		e.Buffer(0, g.srcB)
		e.Buffer(1, g.fullB)
		e.Buffer(2, g.outB)
		metal.Constant(e, 3, &p)
		e.Dispatch(w, h)

		e.Use(g.fill)
		e.Buffer(0, g.outB)
		metal.Constant(e, 1, &p)
		e.Dispatch(h)
	}); err != nil {
		return err
	}
	out := asWords(g.outM)
	for y := 0; y < h; y++ {
		copy(dst[y*dstStride:y*dstStride+2*w], out[y*2*w:(y+1)*2*w])
	}
	return nil
}

// newConverter picks the best path this machine can actually run, and says
// which one it picked and why.
func newConverter(modelPath string, maxShift, radius int, logf func(string, ...any)) converter {
	if modelPath != "" {
		g, err := newGPUConverter(modelPath, maxShift, radius)
		if err == nil {
			return g
		}
		logf("  the accelerated path is unavailable: %v", err)
	} else {
		logf("  no depth model given (-model); using the estimate from the picture")
	}
	return &cueConverter{maxShift: maxShift, radius: radius}
}
