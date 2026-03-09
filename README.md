# foxLyrionDSP

**Go-based DSP engine for Lyrion Music Server**

This is the core signal processing engine that powers the [SqueezeDSP plugin](https://github.com/Foxenfurter/SqueezeDSP). It runs as a standalone binary in the Lyrion/Squeezebox transcoding chain, receiving a raw audio stream via stdin and writing the processed stream to stdout.

> **End users** — you do not need this repository. Install the plugin directly via the Lyrion third-party plugins list. See the [SqueezeDSP repository](https://github.com/Foxenfurter/SqueezeDSP) for installation instructions.

---

## Overview

foxLyrionDSP is invoked by Lyrion's transcoding system as part of a pipeline, for example:

```
flac -dcs -- $FILE$ | foxLyrionDSP --id="xx:xx:xx:xx:xx:xx" --wav=true --wovo=true --d=24 | flac -cs -0 --totally-silent -
```

It reads settings for the given player ID from the SqueezeDSP configuration, applies the configured DSP chain, and passes the processed audio downstream.

---

## Features

- **Parametric Equaliser** — Peak, High Pass, Low Pass, High Shelf, Low Shelf filters with configurable frequency, gain, and Q
- **FIR Convolution** — room correction and impulse response convolution via WAV files, with automatic resampling via SoX to match the incoming sample rate
- **Preamp** — gain reduction to prevent clipping, particularly important when using convolution
- **Balance** — left/right channel level adjustment
- **Width** — stereo width control
- **Delay** — single-channel delay in milliseconds
- **Loudness** — bass and treble compensation at lower listening levels
- **Per-player configuration** — each Lyrion player can have independent settings
- **Preset support** — save and load named configurations
- **Clipping detection** — peak level monitoring with log output

### Supported Audio Formats

Input is processed from the Lyrion transcoding chain. Output is **24-bit FLAC** or **16-bit WAV** depending on player capability.

Audio formats with custom transcoding profiles: `aac, aif, alc, alcx, ape, flc, mov, mp3, mp4, mp4x, mpc, ogg, ogf, ops, spt, wav, wma, wmal, wmap, wvp`

DSD and MQA are not supported.

---

## Architecture

### Package Structure

| Package | Purpose |
|---|---|
| `LyrionDSPFilters` | Filter design and coefficient calculation (PEQ, shelf, pass filters, FIR) |
| `LyrionDSPProcessAudio` | Audio processing pipeline, sample conversion, convolver |
| `LyrionDSPSettings` | Configuration loading and per-player settings management |
| `LyrionTools` | Shared utilities |

### Convolver

The convolver uses a **partitioned FFT** approach with a **ring buffer** to process the audio stream efficiently. Key design decisions:

- A custom minimal FFT library (based on ArgusDusty FFT) with **cached twiddle factors**, optimised for block sizes below 16,386 samples — substantially faster than `scientificgo` in this range
- Partitioned processing minimises the data discarded after FFT/InverseFFT cycles
- Impulse files are resampled on first use (via SoX) and cached for subsequent tracks at the same sample rate
- Filter building at track start uses an **IIR calculation** to combine filters rather than a full convolution, giving approximately **10x faster cold starts**
- Output gain is managed by calculating the inverse of the final impulse's MaxGain and applying it during the gain stage, rather than normalising the impulse itself

**Performance (approximate, streaming with a large room correction impulse):**

| Hardware | Max sample rate | Notes |
|---|---|---|
| Pi 3B | 192kHz | As of v0.1.41 engine rewrite |
| Pi 4 | 192kHz+ | Comfortable |
| Modern PC / server | 192kHz+ | Minimal CPU load |

---

## Building

Requires Go 1.21 or later.

**Build all targets (Windows):**
```bat
Build-All.bat
```

**Build test harness (Windows):**
```bat
Build-Test.bat
```

**Manual cross-compilation:**
```bash
# Linux (ARM - Pi)
GOOS=linux GOARCH=arm GOARM=7 go build -o foxLyrionDSP_arm ./foxLyrionDSP.go

# Linux (ARM64)
GOOS=linux GOARCH=arm64 go build -o foxLyrionDSP_arm64 ./foxLyrionDSP.go

# Linux (x86_64)
GOOS=linux GOARCH=amd64 go build -o foxLyrionDSP ./foxLyrionDSP.go

# Windows
GOOS=windows GOARCH=amd64 go build -o foxLyrionDSP.exe ./foxLyrionDSP.go
```

---

## Testing

A test harness is included for offline development and benchmarking:

```bash
go run dsp_tester.go
```

This allows processing of a local audio file through the DSP chain without requiring a running Lyrion instance.

---

## Command Line Arguments

| Argument | Description |
|---|---|
| `--id` | Player MAC address — used to load the correct per-player settings |
| `--wav` | Input is WAV format |
| `--wovo` | Output WAV format |
| `--d` | Output bit depth (typically `24`) |

---

## Platform Support

Tested on: Windows 10/11, Raspberry Pi 3B/4 (Raspbian), Docker (Linux), Ubuntu (VirtualBox), macOS (VirtualBox), PiCorePlayer.

---

## Related

- [SqueezeDSP Plugin](https://github.com/Foxenfurter/SqueezeDSP) — the Perl/JavaScript plugin that configures and deploys this engine within Lyrion Music Server
- [SqueezeDSP Wiki](https://github.com/Foxenfurter/SqueezeDSP/wiki) — end-user documentation
- [Lyrion Music Server](https://lyrion.org)

---

## License

MIT License — Copyright (c) 2024 Jonathan Fox

[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/Y8Y31UOE3D)
