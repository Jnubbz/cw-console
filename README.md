# CW Console

An open-source CW (Morse) operating console, growing into a full-featured console for the
**Yaesu FT-710**. It decodes, sends and logs CW, runs on Windows, Mac and a headless
Raspberry Pi 5 box, and is free software under the AGPL-3.0.

## What's here today

| | Windows | Mac (Apple Silicon) |
|---|---|---|
| App | Native window (`cmd/cw-decoder`) | Background app + page in your browser (`cmd/cwmac`) |
| Decoders | Classic (Nexus) and AI (DeepCW) | Classic (Nexus) and AI (DeepCW) |
| Sending | DTR/RTS keying on the rig's USB serial port | DTR/RTS keying on the rig's USB serial port |
| Operating aids | Macros, callsign/RST capture, Auto-CQ, auto-reply, match speed | Same |
| Log | ADIF, plus the POTA uploader's file per park and day | Same |

The same `cmd/cwmac` program is also the Raspberry Pi 5 field box ("POTA Pi", run with
`-appliance`): its own Wi-Fi, a phone as the screen, FT8 with WSJT-X's decoder, rig
control over CAT for the FT-710 and FX-4CR, radio profiles, a live POTA activation count,
and A/B over-the-air updates. The box's Buildroot tree isn't in this repository yet.

- **Classic decoder:** a line-for-line Go port of the decoder from
  [Nexus](https://github.com/kd9taw/nexus) by KD9TAW, plus optional speed re-sync and fade
  following ("Improved tracking").
- **AI decoder:** the [DeepCW](https://github.com/e04/deepcw-engine) neural network, run
  by our own Go ONNX interpreter with AVX2 (Intel/AMD) and NEON (Apple Silicon, Pi)
  kernels. No Python, no runtime to install.

## Roadmap: a full FT-710 console

- [ ] Bring the Windows and Mac apps up to the Pi box's features from one codebase
      (CAT rig control, radio profiles, activation count, FT8 where it fits)
- [ ] FT-710 VFO A/B, SPLIT and clarifier over CAT
- [ ] SWR sweep over CAT (short low-power carriers across a band, plotted)
- [ ] CW via the FT-710's own keyer (CAT `KY`) as an option next to DTR/RTS
- [ ] Signed Windows and notarized Mac releases

## Build

Needs [Go 1.24+](https://go.dev/dl/). No cgo, so it cross-builds from any OS.

```bash
scripts/build.sh            # -> dist/CW-Decoder-Windows.zip, dist/CW-Decoder-Mac.zip
VERSION=0.1.0 scripts/build.sh
```

Or one at a time:

```bash
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o CW-Decoder.exe ./cmd/cw-decoder
GOOS=darwin  GOARCH=arm64 go build -o cw-decoder ./cmd/cwmac
go run ./cmd/cwmac          # on a Mac or Linux desktop: opens http://localhost:7373/
```

The AI decoder needs `third_party/deepcw-engine/model.onnx` and `model.onnx.json` in a
`deepcw` folder next to the program (the build script puts them there).

Tests: `go test ./...` (the AI decoder's tests take a couple of minutes).

## Layout

| Path | What |
|---|---|
| `cw/` | Classic decoder, Morse tables, keyer, macros, WAV reading |
| `deepcw/` | ONNX interpreter for the DeepCW model, with SIMD kernels |
| `ops/` | Operating aids: callsign capture, Auto-CQ, ADIF log, POTA activation |
| `cmd/cw-decoder/` | Windows app (Win32 window, waveIn audio, serial keying) |
| `cmd/cwmac/` | Mac app and the Pi box (web page in `web/`) |
| `cmd/cwdecode-file/` | Decode a WAV file from the command line |
| `internal/testgen/` | Makes test recordings |
| `packaging/` | Mac app bundle files and the Windows readme |

## Installing a build

- **Windows:** unzip, run `CW-Decoder.exe` (keep the `deepcw` folder next to it). It
  isn't code-signed yet, so Windows may warn: More info, Run anyway.
- **Mac:** unzip, drag `CW Decoder.app` to Applications, then
  `xattr -dr com.apple.quarantine "/Applications/CW Decoder.app"` (not notarized yet).

Details: `packaging/windows/README.txt`, `packaging/mac/README-Mac.txt`.

## License

AGPL-3.0. Built on Nexus (GPL-3.0), DeepCW (AGPL-3.0) and, on the Pi box, WSJT-X
(GPL-3.0). See [NOTICE.md](NOTICE.md) for credits. **You are responsible for what this
software transmits:** test into a dummy load and stay within your license.

73 de KN0BLE
