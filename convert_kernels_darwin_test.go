package player

import (
	"errors"
	"os"
	"testing"

	"github.com/go-images/depth"
	"github.com/go-macos/metal"
)

// The kernels are a string, compiled at run time by the system compiler, so
// nothing in an ordinary build looks at them at all. Without this test a typo
// reaches a person as a fallback to the worse path, mid-film, with a
// twenty-line compiler error in the log -- which is exactly what happened
// once, over a variable named `half`, that being a TYPE in Metal.
func TestTheKernelsCompileAndEveryOneOfThemIsThere(t *testing.T) {
	dev, err := metal.Default()
	if errors.Is(err, metal.ErrNoDevice) {
		t.Skip("no GPU on this machine")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()

	lib, err := dev.Compile(gpuKernels)
	if err != nil {
		t.Fatalf("the kernels do not compile:\n%v", err)
	}
	defer lib.Close()

	for _, name := range []string{"eyes", "fill", "blurH", "blurV", "upsample"} {
		pipe, err := lib.Pipeline(name)
		if err != nil {
			t.Errorf("kernel %s: %v", name, err)
			continue
		}
		pipe.Close()
	}
	// The negative control: a name that is not there must fail, or the loop
	// above would pass against a library that answers to anything.
	if _, err := lib.Pipeline("thereIsNoSuchKernel"); err == nil {
		t.Error("a kernel that does not exist became a pipeline")
	}
}

// The same property on the ACCELERATED path, which is the one that was running
// when two snapshots at different disparities came out byte for byte identical.
// They did because the film's fortieth frame is almost black -- a depth map
// with no range moves nothing, correctly -- but nothing in that run could tell
// the two explanations apart. This can.
//
// It needs a depth model, which a build machine has no reason to carry:
//
//	XRPLAY_TEST_MODEL=/path/to/Depth.mlpackage go test ./...
func TestTheAcceleratedPathMovesMoreWithMoreDisparity(t *testing.T) {
	model := os.Getenv("XRPLAY_TEST_MODEL")
	if model == "" {
		t.Skip("set XRPLAY_TEST_MODEL to a Core ML depth model to run this")
	}
	const w, h = 256, 160
	stride := w + 7
	src := textured(w, h, stride)
	// Something for the network to find: a block across the lower middle,
	// brighter than its surroundings, with the columns still distinct inside it
	// so a shift remains measurable.
	for y := h / 2; y < 5*h/6; y++ {
		for x := w / 4; x < 3*w/4; x++ {
			c := uint32(x*2%128) + 128
			src[y*stride+x] = 0xFF000000 | c<<16 | c<<8 | c
		}
	}

	count := func(maxShift int, curve []byte) int {
		g, err := newGPUConverter(model, maxShift, 2, curve)
		if err != nil {
			t.Fatalf("the accelerated path would not open: %v", err)
		}
		defer g.close()
		if got := g.describe(); got == "" {
			t.Error("the converter does not say what it is")
		}
		dst := make([]uint32, 2*w*h)
		if err := g.convert(dst, 2*w, src, stride, w, h); err != nil {
			t.Fatal(err)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < 2*w; x++ {
				if dst[y*2*w+x]>>24 == 0 {
					t.Fatalf("a transparent pixel survived at %d,%d", x, y)
				}
			}
		}
		return moved(dst, 2*w, src, stride, w, h)
	}
	small, large := count(8, nil), count(48, nil)
	t.Logf("moved %d pixels at a disparity of 8, %d at 48", small, large)
	if small == 0 {
		t.Fatal("nothing moved at all")
	}
	if large <= small {
		t.Fatalf("a disparity of 48 moved %d pixels, no more than 8 did (%d)", large, small)
	}
}

func TestTheCurveReachesTheKernel(t *testing.T) {
	// The curve travels to the GPU as a table the kernel indexes. If it were
	// not bound, or bound at the wrong index, the kernel would read the
	// identity it is given when no curve is asked for -- and every other test
	// here would still pass.
	model := os.Getenv("XRPLAY_TEST_MODEL")
	if model == "" {
		t.Skip("set XRPLAY_TEST_MODEL to a Core ML depth model to run this")
	}
	const w, h = 256, 160
	stride := w + 7
	src := textured(w, h, stride)
	for y := h / 2; y < 5*h/6; y++ {
		for x := w / 4; x < 3*w/4; x++ {
			c := uint32(x*2%128) + 128
			src[y*stride+x] = 0xFF000000 | c<<16 | c<<8 | c
		}
	}
	run := func(curve []byte) []uint32 {
		// A large disparity on purpose: at a comfortable one the curve is
		// swamped by quantisation, which go-images/depth measures and pins.
		g, err := newGPUConverter(model, 96, 2, curve)
		if err != nil {
			t.Fatal(err)
		}
		defer g.close()
		dst := make([]uint32, 2*w*h)
		if err := g.convert(dst, 2*w, src, stride, w, h); err != nil {
			t.Fatal(err)
		}
		return dst
	}
	plain, curved := run(nil), run(depth.Sigmoid(6))
	diff := 0
	for i := range plain {
		if plain[i] != curved[i] {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("the curve changed nothing; it is not reaching the kernel")
	}
	t.Logf("the curve moved %d of %d pixels", diff, len(plain))

	// The negative control: an IDENTITY table must change nothing at all, or
	// the difference above could be anything -- a rebound allocation, a stale
	// buffer, noise from the network.
	identity := make([]byte, 256)
	for i := range identity {
		identity[i] = byte(i)
	}
	again := run(identity)
	for i := range plain {
		if plain[i] != again[i] {
			t.Fatalf("an identity curve changed the picture at %d", i)
		}
	}
}
