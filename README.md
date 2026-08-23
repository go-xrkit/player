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

## Limits

- **No Matroska.** AVFoundation does not demux MKV or WebM. MP4/MOV/M4V work.
- **No seeking, no audio, no loop.** A file plays through once, silently.
- **macOS only.** The geometry and display logic here are portable and tested
  everywhere; the decoder and window back-ends are not written for Linux or
  Windows yet.

Licence: BSD-3-Clause.
