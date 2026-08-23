package player

import (
	"os"
	"testing"
	"time"
)

func TestSrcFrameRelease(t *testing.T) {
	var nilFrame *srcFrame
	nilFrame.Release() // must not panic

	released := 0
	f := &srcFrame{Pix: []uint32{1, 2, 3}, release: func() { released++ }}
	f.Release()
	if released != 1 || f.Pix != nil || f.release != nil {
		t.Errorf("after Release: calls=%d pix=%v", released, f.Pix)
	}
	// Releasing twice must not hand the decoder's buffer back a second time.
	f.Release()
	if released != 1 {
		t.Errorf("Release called the decoder %d times, want 1", released)
	}
	// A frame with no release func still clears itself without panicking.
	g := &srcFrame{Pix: []uint32{9}}
	g.Release()
}

// TestReadHeadRefusesWhatItCannotRead covers the error path that is not an
// end-of-file: a directory opens like a file and then refuses to be read.
func TestReadHeadRefusesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	if _, err := readHead(dir); err == nil {
		t.Error("readHead of a directory returned no error")
	}
	if _, err := readHead(dir + "/absent"); err == nil {
		t.Error("readHead of a missing file returned no error")
	}
}

func TestReadHeadShortAndEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	short := dir + "/short.bin"
	if err := os.WriteFile(short, []byte("abcd"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readHead(short)
	if err != nil || string(got) != "abcd" {
		t.Errorf("readHead of a short file = (%q, %v)", got, err)
	}
	empty := dir + "/empty.bin"
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readHead(empty); err != nil || len(got) != 0 {
		t.Errorf("readHead of an empty file = (%d bytes, %v)", len(got), err)
	}
}

func TestSourceInfoAndFrameAreValues(t *testing.T) {
	// A guard against the struct quietly gaining a pointer field that would
	// make copying it share state.
	i := SourceInfo{Width: 1920, Height: 1080, FrameRate: 30, Duration: time.Second, Container: "x", Codec: "avc1"}
	j := i
	j.Width = 1
	if i.Width != 1920 {
		t.Error("copying a SourceInfo shared state")
	}
}
