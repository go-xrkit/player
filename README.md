<p align="center"><img src="https://raw.githubusercontent.com/go-xrkit/brand/main/social/go-xrkit.png" alt="go-xrkit" width="720"></p>

# go-xrkit/player

[![Go Reference](https://pkg.go.dev/badge/github.com/go-xrkit/player.svg)](https://pkg.go.dev/github.com/go-xrkit/player)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD%203--Clause-blue.svg)](https://opensource.org/licenses/BSD-3-Clause)
[![CI](https://github.com/go-xrkit/player/actions/workflows/ci.yml/badge.svg)](https://github.com/go-xrkit/player/actions/workflows/ci.yml)

Plays immersive video on XR glasses: 360°, VR180 and 3D films, per eye, full
screen on the glasses' own display.

```
xrplay film.mp4                       # detect everything, find the glasses
xrplay -proj 360 -layout sbs f.mp4    # override the detection
xrplay -screen "Built-in" -mono f.mp4 # one eye on the laptop, to look at it
xrplay -for 10s -snapshot out.png f   # stop after 10s, capture what was shown
```

`CGO_ENABLED=0`. It joins three things that know nothing about each other —
[go-macos/avfoundation](https://github.com/go-macos/avfoundation) for hardware
decoding, [go-xrkit/xrkit](https://github.com/go-xrkit/xrkit) for the geometry,
and [go-widgets/window](https://github.com/go-widgets/window) for a full-screen
window on the right physical display.

## How the stereo actually happens

**There is no SDK involved.** XR glasses expose their 3D mode *as a display
mode*: a VITURE Beast reports 3840x1080 for side-by-side 3D and 1920x1200 for
ordinary 2D. So stereo output is a borderless window covering that display, with
one eye's view in each half — which is all `StereoMode` decides, from arithmetic
on the panel size.

The blind spot is documented rather than hidden: a genuine 32:9 monitor has the
same aspect as two 16:9 eyes, and no arithmetic can separate them. Hence
`-screen` and `-mono`.

**For image quality, put the glasses in their pixel-exact mode.** macOS also
offers scaled modes — a Beast reporting "5120x1600, looks like 2560x800" is
being rendered at 5120x1600 and downsampled onto a 3840x1080 panel, which costs
sharpness for nothing.

## What it guesses, and why it says so

None of a file's immersive geometry is reliably recorded in its container, and
the conventions that exist are conventions of *file naming*. `Detect` therefore
reasons from the name first and the frame shape second, and **records its
reasoning**:

```
content   equirectangular 360x180, mono eyes
  because: no stereo marker in the name and no stereo aspect; 2:1 eye, so equirectangular 360
```

A viewer that silently mis-detects 180° content as 360° shows the world squeezed
into half the view and gives the user nothing to act on. Every axis is
overridable with `-proj`, `-layout` and `-swap`.

One ambiguity is worth knowing: real VR180 stores 180°x180° per eye, which is
**square**, so a side-by-side VR180 frame is 2:1 overall — indistinguishable by
aspect from a monoscopic sphere. Only the name tells them apart. A 4:1 frame, by
contrast, is a full sphere per eye: stereoscopic 360.

## Why there is no head tracking

The VITURE Beast's IMU is **not reachable over HID**. Its three HID interfaces
open without complaint and `SetReport` reports success, and they emit nothing at
all — proven against a control run that captured 481 reports from the same
reader on other devices. The newer generations use USB control transfers, which
is a separate job. See [go-macos/iokit](https://github.com/go-macos/iokit).

That absence is why the warp is a precomputed table: with a fixed orientation the
sampling is the same on every frame, and the whole two-eye pass costs 3 ms at
4K (8 ms at 8K) against a 16.6 ms budget at 60 Hz. A GPU warp is not needed
until head tracking arrives.

## Two ways in, chosen by what the file is

| container | path | notes |
|---|---|---|
| MP4, MOV, M4V | `AVPlayer` | demux, decode **and sound**, with a clock of its own |
| MP4 that AVFoundation refuses | the demuxed path, **automatically** | silent, but it plays |
| **MKV, WebM** | [`go-avkit/avkit/container`](https://github.com/go-avkit/avkit) demux + [`go-macos/videotoolbox`](https://github.com/go-macos/videotoolbox) | AVFoundation refuses Matroska outright |

The choice is made from what the file **is**, not by trying AVFoundation and
falling back on its refusal — a fallback would also swallow the failures that
are genuinely about a broken file.

The Matroska path costs memory: `avkit`'s reader is handed a byte slice, so a
feature film is **resident while it plays** (about 2 GB for a 1 h 32 encode).
That is why it is chosen only for the containers AVFoundation will not open.

It also **reorders**. VideoToolbox emits frames in decoding order, so a stream
with B-frames hands them over out of presentation order; showing them as they
arrive plays the picture with a stutter that reads as a decode fault and is not
one. Up to 8 frames are held back and emitted by timestamp.

## Controls

The transport bar appears when the pointer moves and hides after three idle
seconds, the way every video player does it. Every element is a go-widgets
widget and every glyph an Iconoir drawing through `Button`'s icon seam — nothing
here paints a pixel of its own, because a hand-drawn bar is a private set of
shapes that no theme, no HiDPI scale and no accessibility walk knows about.

```
  37:12  ──────────────────────────●───   43:58
            ⏮   ⏪15   ⏸   ⏩15   🔊
```

It sits over a dark scrim: white text on a bright shot is unreadable, and XR
optics wash blacks out, so the veil is tuned darker than a desk monitor would
need. In a side-by-side 3D mode it stays in the **left eye's half** — drawn
across the panel it would appear twice, at different depths, and read as a
double image.

**The pointer has to be over the glasses' display for the bar to appear.** That
is how a full-screen player on a second monitor behaves; the keyboard works
wherever the pointer is.


| key | |
|---|---|
| `space` / `k` | pause, resume |
| `←` `→` / `j` `l` | seek 10 s |
| `↑` `↓` | volume |
| `Esc` / `q` | quit |

Both the arrows and `j`/`k`/`l` are bound: `j`/`k`/`l` is what every video player
has taught people, and the arrows are what everyone tries first. An unknown key
does **nothing** — it used to quit, which meant brushing the keyboard closed the
film, and a viewer in a headset cannot see what they pressed.

Seeking and volume need a clocked source, so they apply to the AVPlayer path.
Pause works everywhere: a pull decoder is stopped and the stopped time is given
back, so a resumed video carries on instead of racing to catch up.

## Opening is not working

A source is accepted only once it has actually **produced a picture**.

That distinction earns its keep on real files. A 7200x3600 VR180 recording stored
as `hev1` — HEVC with its parameter sets in the *bitstream* rather than the
sample description — is **opened by AVPlayer without complaint and then never
becomes ready**, while `AVAssetReader` fails it outright. VideoToolbox decodes
the same file happily from the parameter sets the demuxer recovers. So when the
first candidate cannot produce a frame, the demuxed path is tried, and the
fallback says so in the log:

```
  AVFoundation could not play this file (no picture within 10s; the item never
  became ready); trying the demuxer
  7200x3600  59.939 fps  43m58.358s
  via mp4 (avkit demux + VideoToolbox)
  content   equirectangular 180x180, side-by-side eyes
  view      100.0% of the view covered
```

When the second path fails too, **both** failures are reported. A fallback that
hid the first error would turn a genuinely broken file into a puzzle.

## The sound is the clock

On the demuxed path the video is timed against **how much sound has actually
left the speaker**, not against the wall clock. That is the rule every serious
player follows, for a reason that is not symmetric: **the ear notices a drift the
eye ignores**, so the picture follows the sound and never the other way round.
Over a feature-length film a wall clock would walk away from the audio audibly.

Decoding stays about a second ahead of what is heard, no further. A decoder that
raced to the end would hold the whole film's PCM in the queue, and a pause would
then take the rest of the film to be heard.

A file with no audio track, or one in a codec this cannot decode, is **not an
error**: it plays silently and the log says which of the two it was. Silence with
no explanation is the thing worth avoiding.

5.1 is mixed down to stereo. Sent to a headset unmixed it loses the centre
channel, which is where the dialogue is.

## Limits

- **No loop.** A file plays through once. Seeking needs a clocked source, so it
  works on the AVPlayer path and not on the demuxed one.
- **AV1, VP9 and VP8 in Matroska are not decoded** — H.264 and HEVC only.
- **macOS only.** The geometry and display logic here are portable and tested
  everywhere; the decoder and window back-ends are not written for Linux or
  Windows yet.

Licence: BSD-3-Clause.
