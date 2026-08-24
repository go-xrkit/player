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
		return openAVPlayer(path)
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

// ---------------------------------------------------------------------------
// AVPlayer: MP4, MOV, M4V — with sound, and a clock of its own.
// ---------------------------------------------------------------------------

// clocked is a source that owns its own clock: it renders audio through the
// system output, drops video to stay in sync, and answers where in the file it
// is. A source without it is a decoder the player must time itself.
//
// The distinction is not cosmetic. A clocked source must be opened AND polled
// from the process main thread — AVFoundation loads through the main dispatch
// queue and only the main thread's run loop drains it — so it is driven from the
// paint callback, not from a decode goroutine. The two shapes cannot share one
// loop, which is why this interface exists rather than a flag.
type clocked interface {
	source
	// Play starts or resumes, with sound.
	Play()
	// Pause stops the clock where it is.
	Pause()
	// CurrentTime is where the clock is.
	CurrentTime() time.Duration
	// SetVolume takes 0 to 1.
	SetVolume(v float64)
	// Pump runs the MAIN thread's run loop for about d. It must be called only
	// before a window system takes the main run loop over, never after.
	Pump(d time.Duration)
}

type avPlayerSource struct {
	p    *avfoundation.Player
	info SourceInfo
}

// openAVPlayer opens a file for real-time playback.
//
// Loading is asynchronous through the main dispatch queue, so the run loop is
// pumped while waiting. Sleeping instead would not load the file — and worse,
// the player would still ANSWER, echoing back whatever time it was last handed,
// which is exactly how a binding that has opened nothing passes for one that
// works.
func openAVPlayer(path string) (source, error) {
	p, err := avfoundation.OpenPlayer(path)
	if err != nil {
		return nil, err
	}
	i := p.Info()
	return &avPlayerSource{p: p, info: SourceInfo{
		Width: i.Width, Height: i.Height, FrameRate: i.FrameRate,
		Duration: i.Duration, Container: "MP4/MOV (AVPlayer, with sound)",
	}}, nil
}

func (s *avPlayerSource) Info() SourceInfo           { return s.info }
func (s *avPlayerSource) Close() error               { return s.p.Close() }
func (s *avPlayerSource) Play()                      { s.p.Play() }
func (s *avPlayerSource) Pause()                     { s.p.Pause() }
func (s *avPlayerSource) CurrentTime() time.Duration { return s.p.CurrentTime() }
func (s *avPlayerSource) SetVolume(v float64)        { s.p.SetVolume(v) }
func (s *avPlayerSource) Pump(d time.Duration)       { s.p.Pump(d) }

// Next is a POLL, not a pull: it returns (nil, nil) when the clock has not moved
// on to a new picture yet, which is the common case at a display refresh rate
// higher than the video's. A caller draws the previous frame again.
func (s *avPlayerSource) Next() (*srcFrame, error) {
	f, err := s.p.TryFrame()
	if err != nil || f == nil {
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
