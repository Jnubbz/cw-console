# CW Console

An open-source CW (Morse) operating console, growing into a full-featured console for the
**Yaesu FT-710**. It decodes, sends and logs CW, runs on Windows, Mac and a headless
Raspberry Pi 5 box, and is free software under the AGPL-3.0.

**Download:** the latest Windows and Mac builds are on the
[Releases page](https://github.com/Jnubbz/cw-console/releases/latest). Once installed, the app
updates itself.

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

- [x] FT-710 **Rig** sheet (Mac and Pi box): VFO A/B, A⇄B, A→B, B→A, split (and Up 1/Up 2),
      mode, clarifier RX/TX and offset, power, tuner
- [x] SWR sweep: short 5-10 W CW carriers across a band on the key line, the radio's SWR
      meter read over CAT at each step, plotted; the radio is put back exactly as it was
- [ ] The Rig sheet on Windows (the Windows app is a native window; needs its own panel)
- [ ] Bring the Windows and Mac apps up to the Pi box's features from one codebase
      (band switching over CAT, radio profiles, activation count, FT8 where it fits)
- [ ] Load macros into the FT-710's keyer memories (`KM`) and play them (`KY`). Yaesu's `KY`
      only plays memories; it can't key typed text, so live CW stays on DTR/RTS
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

Tests: `go test ./...` (the AI decoder's tests take a couple of minutes; `-short` skips the
slowest). `testdata/samples/` holds four real-world-style recordings (clean 20 WPM, noisy,
35 WPM, QRM) that both decoders must copy exactly. CI runs everything on Linux, Mac and
Windows for each pull request and builds the zips; pushing a `v*` tag publishes a release.

## Layout

| Path | What |
|---|---|
| `cw/` | Classic decoder, Morse tables, keyer, macros, WAV reading |
| `deepcw/` | ONNX interpreter for the DeepCW model, with SIMD kernels |
| `ops/` | Operating aids: callsign capture, Auto-CQ, ADIF log, POTA activation |
| `contest/` | Country/zone lookup (AD1C's cty.dat), CQ WW scoring, Cabrillo |
| `rig/ft710/` | FT-710 CAT: commands, answers, meter calibration, SWR sweep (no I/O, fully tested) |
| `cmd/cw-decoder/` | Windows app (Win32 window, waveIn audio, serial keying) |
| `cmd/cwmac/` | Mac app and the Pi box (web page in `web/`) |
| `cmd/cwdecode-file/` | Decode a WAV file from the command line |
| `internal/testgen/` | Makes test recordings |
| `testdata/samples/` | Sample recordings with known text (decoder regression tests) |
| `packaging/` | Mac app bundle files and the Windows readme |

## Home station first

The main screen keeps what you use at hand: a waterfall (spectrum above, ~5 Hz history
below, the decoder's passband shaded; click a signal to tune), the band strip, the decoded
text and the sender; on a wide screen a side column has the **radio** (frequency, band keys,
split, A/B, RIT, power: the Rig sheet's **More...** has the rest and the SWR sweep), the
**decoder** (classic or AI, sensitivity) and **who's on the band**. Dark with blue accents;
POTA mode switches to the green field look.

CW Console is a full CW console for the home station. POTA activating is an option:
**Setup > Operating > POTA activation mode** shows the park and activation-count bar
(always on for the POTA Pi box).

**Band strip** (Setup > Operating > Spots): under the spectrum, the CW part of the band the
radio is on (or one you pick), with Reverse Beacon Network skimmer spots (green, brighter =
stronger) and POTA activators (brass, "P"). Hover for the details; click to tune the radio
there in CW and put the call in the Call box. On the right, **Heard by N skimmers**: the RBN
copying your own call (click for the list). RBN logs in with MYCALL from the macros
(`telnet.reversebeacon.net:7000`); POTA spots come from `api.pota.app` once a minute.

## The Rig sheet (FT-710)

**Rig** in the top bar talks to the FT-710 over CAT on its *Enhanced* COM port (CAT-1, the
first of its two USB serial ports; the key port is the second). Setup > Radio > **CAT port**
is Auto by default: the app asks each USB serial port for the FT-710's ID. The radio's
CAT-1 RATE must be 38400 (its factory setting; Setup menu > OPERATION SETTING > GENERAL).
Quit WSJT-X or other CAT programs first: only one program can have the port open.

The **SWR sweep** keys the radio the way CW does (DTR/RTS on the key port, break-in on), so
turn on Transmit first. It sends a 250 ms carrier at 5 or 10 W per step, in CW (allowed
everywhere in every US HF band), and reads the SWR meter. Esc or Stop ends it; the radio's
frequency, mode, power, split, clarifier and break-in are always put back. Only sweep where
your license lets you transmit, and listen first.

## Contesting (CQ WW DX CW)

Setup > Operating > **Contest: CQ WW DX (CW)**. Set the zone you send (Florida: 5),
power, and whether you're **assisted**: CQ WW counts a CW decoder, RBN and spotting as
QSO finding assistance, so with them on you enter Single Operator Assisted (the default).

- **Zone box** next to the Call box. Left empty, the zone the decoder heard after their
  report (green) or the country file's guess (grey) is logged.
- As you type a call: the country, the points, **NEW ZONE / NEW COUNTRY** on this band,
  and **DUPE**. The band strip greys out dupes and shows new multipliers in green.
- **Enter sends message** (Space jumps to the Zone box). **Run:** an empty Call box sends
  CQ, a call sends the exchange, then (with their zone) TU and logs it. **S&P:** a call
  sends your call, then TU + your exchange and logs it.
- Contest macros (`contest-macros.txt`, Setup > Edit contest macros) replace the F-keys
  while Contest mode is on; `{ZONE}` is your zone.
- The side column keeps score (points x (zones + countries), per band) and the rate.
- The contest log starts fresh by itself at 0000Z when the contest starts; contacts made
  before it (practice) stay in your regular log. **Save Cabrillo** writes the log to upload
  at cqww.com/logcheck within 48 hours. Every contact is also in the regular ADIF log
  (with `CONTEST_ID` and `CQZ`).
- Countries come from AD1C's `cty.dat`. A copy is built in; put a newer one from
  country-files.com in the data folder before the contest and restart.

## Updates

Every merge to `main` publishes a release (`v0.2.<build>`, `.github/workflows/release.yml`).
The apps look for it a few seconds after starting and every 6 hours:

- **Mac:** a bar says the new version is ready, with **Install and restart**; Setup >
  Updates has **Check for updates**. It downloads, swaps the app in place and restarts; the
  page reloads itself.
- **Windows:** a Yes/No box; progress in the title bar; the app restarts into the new one.

Releases are trusted only if signed: `latest.json` lists each zip's SHA-256 and is signed
with an Ed25519 key kept as the repository secret `UPDATE_SIGNING_KEY`; the apps carry the
public key (`internal/selfupdate`). A manifest with a bad signature, or a zip whose size or
hash doesn't match it, is refused. Your settings, macros and logs aren't touched.

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
