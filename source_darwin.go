//go:build darwin

package player

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/go-avkit/avkit/container"
	"github.com/go-macos/avfoundation"
	"github.com/go-macos/videotoolbox"
)

// openSource picks the path that can open path, and says which one it was in
// the resulting SourceInfo.Container.
//
// The choice is made from what the file IS, not by trying AVFoundation and
// falling back on its refusal: a fallback would also swallow the failures that
// are genuinely about a broken file.
func openSource(path string) (source, error) {
	head, err := readHead(path)
	if err != nil {
		return nil, err
	}
	switch container.Sniff(head) {
	case container.FormatMatroska:
		return openMatroska(path)
	default:
		return openAVFoundation(path)
	}
}

// ---------------------------------------------------------------------------
// AVFoundation: MP4, MOV, M4V.
// ---------------------------------------------------------------------------

type avSource struct {
	r    *avfoundation.Reader
	info SourceInfo
}

func openAVFoundation(path string) (source, error) {
	r, err := avfoundation.Open(path)
	if err != nil {
		return nil, err
	}
	i := r.Info()
	return &avSource{r: r, info: SourceInfo{
		Width: i.Width, Height: i.Height, FrameRate: i.FrameRate,
		Duration: i.Duration, Container: "MP4/MOV (AVFoundation)",
	}}, nil
}

func (s *avSource) Info() SourceInfo { return s.info }
func (s *avSource) Close() error     { return s.r.Close() }

func (s *avSource) Next() (*srcFrame, error) {
	f, err := s.r.NextFrame()
	if err != nil {
		return nil, err
	}
	return &srcFrame{
		Width: f.Width, Height: f.Height,
		StrideWords: f.Stride / 4,
		PTS:         f.PTS,
		Pix:         asWords(f.Pix),
		release:     f.Release,
	}, nil
}

// ---------------------------------------------------------------------------
// VideoToolbox over an avkit demux: Matroska and WebM.
// ---------------------------------------------------------------------------

type vtSource struct {
	sess    *videotoolbox.Session
	samples []videotoolbox.Sample
	next    int
	pending []*videotoolbox.Frame
	drained bool
	info    SourceInfo
	// data is the whole file. avkit's reader is handed a byte slice, so a
	// Matroska film is resident for as long as it plays: about 2 GB for a
	// feature. That is a real cost and the reason this path is chosen only for
	// the containers AVFoundation refuses.
	data []byte
}

func openMatroska(path string) (source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r, err := container.NewReader(data)
	if err != nil {
		return nil, fmt.Errorf("player: cannot demux %s: %w", path, err)
	}
	file := r.File()

	var track *container.Track
	for i := range file.Tracks {
		if file.Tracks[i].Kind == container.Video {
			track = &file.Tracks[i]
			break
		}
	}
	if track == nil {
		return nil, fmt.Errorf("player: %s has no video track", path)
	}
	cfg, err := r.TrackConfig(track.ID)
	if err != nil {
		return nil, fmt.Errorf("player: cannot read the track configuration of %s: %w", path, err)
	}
	codec, ok := codecOf(cfg.Codec)
	if !ok {
		return nil, fmt.Errorf("player: %s carries %q, which this path cannot decode (H.264 and HEVC only)",
			path, cfg.Codec)
	}

	raw, err := r.Samples(track.ID)
	if err != nil {
		return nil, fmt.Errorf("player: cannot read the samples of %s: %w", path, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("player: %s has an empty video track", path)
	}

	sess, err := videotoolbox.New(videotoolbox.Config{
		Codec: codec,
		VPS:   cfg.VPS, SPS: cfg.SPS, PPS: cfg.PPS,
		Width: cfg.Width, Height: cfg.Height,
	})
	if err != nil {
		return nil, fmt.Errorf("player: cannot start a decoder for %s: %w", path, err)
	}

	s := &vtSource{
		sess:    sess,
		samples: timedSamples(raw, cfg.Timescale),
		data:    data,
		info: SourceInfo{
			Width: cfg.Width, Height: cfg.Height,
			Duration:  time.Duration(file.DurationSeconds() * float64(time.Second)),
			Container: "Matroska (avkit demux + VideoToolbox)",
			Codec:     cfg.Codec,
		},
	}
	if d := s.info.Duration; d > 0 {
		s.info.FrameRate = float64(len(raw)) / d.Seconds()
	}
	return s, nil
}

func (s *vtSource) Info() SourceInfo { return s.info }

func (s *vtSource) Close() error {
	videotoolbox.ReleaseAll(s.pending)
	s.pending = nil
	s.data = nil
	return s.sess.Close()
}

// Next feeds samples until it can emit the earliest frame it holds.
func (s *vtSource) Next() (*srcFrame, error) {
	for len(s.pending) < reorderDepth && s.next < len(s.samples) {
		frames, err := s.sess.Decode(s.samples[s.next])
		s.next++
		if err != nil {
			return nil, fmt.Errorf("player: decode failed at sample %d: %w", s.next-1, err)
		}
		s.pending = append(s.pending, frames...)
	}
	if s.next >= len(s.samples) && !s.drained {
		// The decoder holds frames back too; ask for them before concluding.
		frames, err := s.sess.Flush()
		s.drained = true
		if err != nil {
			return nil, fmt.Errorf("player: flush failed: %w", err)
		}
		s.pending = append(s.pending, frames...)
	}
	if len(s.pending) == 0 {
		return nil, io.EOF
	}
	var f *videotoolbox.Frame
	f, s.pending = popEarliest(s.pending)
	return &srcFrame{
		Width: f.Width, Height: f.Height,
		StrideWords: f.Stride / 4,
		PTS:         f.PTS,
		Pix:         asWords(f.Pix),
		release:     f.Release,
	}, nil
}
