package player

import "unsafe"

// A frame is words to the warp tables and bytes to everything else, over the
// same memory. These moved out of the darwin file when the 2D-to-3D conversion
// needed them too: it is portable, and it works in bytes because that is what
// an image package speaks.

// asWords views a byte slice as 32-bit words. The decoder's buffers are at least
// 16-byte aligned, so the alignment a uint32 needs is satisfied.
func asWords(b []byte) []uint32 {
	if len(b) < 4 {
		return nil
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(&b[0])), len(b)/4)
}

// asBytes is the reverse, for handing a composed frame to the toolkit.
func asBytes(w []uint32) []byte {
	if len(w) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&w[0])), len(w)*4)
}
