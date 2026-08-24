package player

import (
	"io"
	"os"
	"time"

	"github.com/go-avkit/avkit/container"
	"github.com/go-macos/videotoolbox"
)

// SourceInfo describes the video a source yields.
type SourceInfo struct {
	Width, Height int
	FrameRate     float64
	Duration      time.Duration
	// Container names what the file is and which path opened it, for the log.
	Container string
	// Codec names the video codec, when the source knows it.
	Codec string
}

// srcFrame is one decoded frame, with its pixels still owned by the decoder.
type srcFrame struct {
	Width, Height int
	// StrideWords is the row length in 32-BIT WORDS, which is what the warp
	// tables index by. The decoders report bytes; the conversion happens once,
	// here, rather than at every use.
	StrideWords int
	PTS         time.Duration
	Pix         []uint32
	release     func()
}

// Release hands the buffer back to whichever decoder produced it.
func (f *srcFrame) Release() {
	if f == nil || f.release == nil {
		return
	}
	f.release()
	f.release = nil
	f.Pix = nil
}

// source yields decoded frames in DISPLAY order.
//
// Two exist, because one library cannot open everything: AVFoundation decodes a
// file end to end but will not demux Matroska, and the VideoToolbox path demuxes
// Matroska but is handed coded frames one at a time. Which one opens a file is
// decided by what the file IS, not by trying one and seeing.
type source interface {
	Info() SourceInfo
	// Next returns the next frame to display, or io.EOF at the end of the file.
	// The caller must Release every frame it receives.
	Next() (*srcFrame, error)
	Close() error
}

// reorderDepth is how many decoded frames are held back to put them in display
// order.
//
// VideoToolbox emits frames in DECODING order, and a stream with B-frames hands
// them over out of presentation order — so showing them as they arrive plays the
// picture with a stutter that looks like a decode fault and is not one. H.264
// allows up to 16 frames of reordering but real encoders use far fewer; 8 covers
// every stream met so far and costs about 30 MB at 720p, which is the price of
// holding decoder buffers rather than copying them.
const reorderDepth = 8

// popEarliest removes and returns the frame with the smallest presentation
// timestamp, which is the one to show next.
//
// A linear scan beats a heap here: the slice is at most [reorderDepth] long, and
// being obviously correct matters more than being asymptotically clever in a
// function whose failure mode is a video that plays in the wrong order.
func popEarliest(pending []*videotoolbox.Frame) (*videotoolbox.Frame, []*videotoolbox.Frame) {
	if len(pending) == 0 {
		return nil, pending
	}
	best := 0
	for i := 1; i < len(pending); i++ {
		if pending[i].PTS < pending[best].PTS {
			best = i
		}
	}
	f := pending[best]
	return f, append(pending[:best], pending[best+1:]...)
}

// containerName renders a demuxed format for the log, so a viewer can see WHICH
// path opened the file. That matters most when an MP4 lands on the demuxed path
// because AVFoundation refused it: without it, the log would say nothing about
// why the file behaves differently from every other MP4.
func containerName(format string) string {
	if format == "" {
		return "demuxed"
	}
	return format
}

// codecOf maps a demuxer's sample entry to a decoder codec.
func codecOf(fourcc string) (videotoolbox.Codec, bool) {
	switch fourcc {
	case "avc1", "avc3":
		return videotoolbox.H264, true
	case "hvc1", "hev1":
		return videotoolbox.HEVC, true
	}
	return 0, false
}

// timedSamples turns demuxed samples into decoder samples, computing each
// presentation timestamp from the decoding timeline.
//
// A demuxer states a sample's DURATION, not its position: decoding time is the
// running sum, and presentation time is that plus the composition offset a
// reordered stream carries. Getting this wrong is not visible as a wrong
// picture, only as a wrong pace — which is far harder to notice, and harder
// still to attribute.
func timedSamples(raw []container.Sample, timescale uint32) []videotoolbox.Sample {
	if timescale == 0 {
		timescale = 1000
	}
	out := make([]videotoolbox.Sample, len(raw))
	var dts int64
	for i, s := range raw {
		out[i] = videotoolbox.Sample{
			Data:     s.Data,
			PTS:      ticks(dts+int64(s.CompositionOffset), timescale),
			Duration: ticks(int64(s.Duration), timescale),
		}
		dts += int64(s.Duration)
	}
	return out
}

// ticks converts a count in a timescale to a duration.
func ticks(n int64, timescale uint32) time.Duration {
	return time.Duration(float64(n) / float64(timescale) * float64(time.Second))
}

// sniffBytes is how much of a file container.Sniff needs. Its MP4 test reads a
// box header and its Matroska test an EBML header, both within the first few
// dozen bytes; 64 KiB is generous and costs one read.
const sniffBytes = 64 << 10

// readHead reads the start of a file, for sniffing what it is.
func readHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, sniffBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}
