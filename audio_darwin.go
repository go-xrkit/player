//go:build darwin

package player

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-avkit/avkit/container"
	"github.com/go-macos/audiotoolbox"
)

// audioTrack decodes and plays a demuxed audio track, and answers where it has
// got to.
//
// It exists because the Matroska path decodes video only: AVFoundation brings
// its own sound and this one has to be built. What it gives back beyond the
// sound is a CLOCK, and that is the more important half — the ear notices a
// drift the eye ignores, so a player times its video against the audio and never
// the other way round.
type audioTrack struct {
	dec    *audiotoolbox.Decoder
	out    *audiotoolbox.Player
	packs  []audiotoolbox.Packet
	stopCh chan struct{}
	once   sync.Once

	mu     sync.Mutex
	failed error
}

// audioLead is how far ahead of the speaker the feeder tries to stay. Too little
// and the queue starves into a gap; too much and a pause takes that long to be
// heard. A second is the usual compromise.
const audioLead = time.Second

// openAudio builds the audio side of a demuxed file, or reports why it cannot.
//
// A file with no audio track, or one in a codec this cannot decode, is NOT an
// error: it plays silently, as it did before there was any sound at all. Only a
// track that should work and does not is worth failing over.
func openAudio(r *container.Reader, file *container.File) (*audioTrack, string) {
	var track *container.Track
	for i := range file.Tracks {
		if file.Tracks[i].Kind == container.Audio {
			track = &file.Tracks[i]
			break
		}
	}
	if track == nil {
		return nil, "no audio track"
	}
	cfg, err := r.TrackConfig(track.ID)
	if err != nil {
		return nil, fmt.Sprintf("cannot read the audio track configuration: %v", err)
	}
	codec, ok := audiotoolbox.CodecFor(cfg.Codec)
	if !ok {
		return nil, fmt.Sprintf("audio is %q, which this path cannot decode", cfg.Codec)
	}

	acfg := audiotoolbox.Config{
		Codec:           codec,
		SampleRate:      cfg.SampleRate,
		Channels:        cfg.Channels,
		AudioObjectType: cfg.AudioObjectType,
		CodecConfig:     cfg.CodecConfig,
		// Two channels out whatever comes in: these are glasses with a stereo
		// pair, and a 5.1 track played into them unmixed loses the centre, which
		// is where the dialogue is.
		OutputChannels: 2,
	}
	dec, err := audiotoolbox.NewDecoder(acfg)
	if err != nil {
		return nil, fmt.Sprintf("cannot build an audio decoder: %v", err)
	}
	out, err := audiotoolbox.NewPlayer(audiotoolbox.PlayerConfigFor(dec.Config()))
	if err != nil {
		dec.Close()
		return nil, fmt.Sprintf("cannot open the audio output: %v", err)
	}

	raw, err := r.Samples(track.ID)
	if err != nil {
		out.Close()
		dec.Close()
		return nil, fmt.Sprintf("cannot read the audio samples: %v", err)
	}

	a := &audioTrack{dec: dec, out: out, stopCh: make(chan struct{})}
	a.packs = make([]audiotoolbox.Packet, len(raw))
	ts := cfg.Timescale
	if ts == 0 {
		ts = 1000
	}
	var dts int64
	for i, s := range raw {
		a.packs[i] = audiotoolbox.Packet{Data: s.Data, PTS: ticks(dts, ts)}
		dts += int64(s.Duration)
	}
	return a, fmt.Sprintf("%s %d ch %d Hz, %d packets", cfg.Codec, cfg.Channels, cfg.SampleRate, len(raw))
}

// Start begins playback and the feeding goroutine.
func (a *audioTrack) Start() error {
	if err := a.out.Start(); err != nil {
		return err
	}
	go a.feed()
	return nil
}

// feed decodes packets and hands the PCM to the output, staying [audioLead]
// ahead of what has been heard.
//
// It never runs further ahead than that. A decoder that races to the end would
// hold the whole film's PCM in the queue, and a pause would then take the rest
// of the film to be heard.
func (a *audioTrack) feed() {
	for _, p := range a.packs {
		select {
		case <-a.stopCh:
			return
		default:
		}
		for a.out.Queued()-a.out.Played() > audioLead {
			select {
			case <-a.stopCh:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		buf, err := a.dec.Decode(p)
		if err != nil {
			a.fail(err)
			return
		}
		if len(buf.PCM) == 0 {
			continue
		}
		// PCM aliases the decoder's scratch and the next Decode overwrites it,
		// so it goes to the output before the loop turns.
		if _, err := a.out.Write(buf.PCM); err != nil {
			a.fail(err)
			return
		}
	}
}

func (a *audioTrack) fail(err error) {
	a.mu.Lock()
	if a.failed == nil {
		a.failed = err
	}
	a.mu.Unlock()
}

// Err reports the first failure, if any.
func (a *audioTrack) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.failed
}

// Played is how much sound has actually left the speaker. This is the clock the
// video follows.
func (a *audioTrack) Played() time.Duration { return a.out.Played() }

// Close stops everything. It is safe to call more than once.
func (a *audioTrack) Close() {
	a.once.Do(func() {
		close(a.stopCh)
		a.out.Stop()
		a.out.Close()
		a.dec.Close()
	})
}
