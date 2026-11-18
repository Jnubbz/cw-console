# Credits and licenses

CW Console is licensed under the **GNU Affero General Public License v3.0** (see
[LICENSE](LICENSE)). It is built on these projects:

- **Nexus** by KD9TAW — the classic CW decoder (`cw/`) is a line-for-line Go port of
  Nexus's `cw.rs`, `cw_decode.rs` and `spectrum.rs`.
  <https://github.com/kd9taw/nexus> — GPL-3.0-only.
- **DeepCW** by e04 — the AI decoder's neural-network model
  (`third_party/deepcw-engine/model.onnx`), run by this project's own Go ONNX
  interpreter (`deepcw/`). <https://github.com/e04/deepcw-engine> — AGPL-3.0-only.
- **WSJT-X** by Joe Taylor K1JT and the WSJT Development Group — FT8 decoding
  runs a local install's `jt9`. That binary is not in this repository, and the
  program does not redistribute WSJT-X. Transmit audio is this program's own
  encoder (the published 77-bit packing, CRC-14, (174,91) LDPC code, and GFSK).
  `CWBOX_FT8TX` can point at the Pi image's `ft8tx` helper instead.
  <https://wsjt.sourceforge.io/> — GPL-3.0.
- **Hamlib** — the FT-710's CAT command details and its meter calibration tables
  (`rig/ft710/`) were checked against Hamlib's FT-710 driver (`rigs/yaesu/newcat.c`,
  `ft710.h`; SWR calibration by G3VPX, power calibration from FLRig by W1HKJ). No Hamlib
  code is included. <https://github.com/Hamlib/Hamlib> — LGPL-2.1.
- **cty.dat** by Jim Reisert AD1C — the country and CQ zone file (`contest/cty.dat`, the
  2020-04-05 edition as distributed with Tlf, <https://github.com/Tlf/tlf>). Current
  editions: <https://www.country-files.com>.
- **IBM Plex** fonts (`cmd/cwmac/web/fonts/`) — SIL Open Font License 1.1
  (`OFL-IBM-Plex.txt` in that folder).
- "Parks on the Air" and POTA are trademarks of Parks on the Air, Inc. This project is
  not affiliated with or endorsed by them, and does not include their logo.

GPL-3.0 code may be combined with AGPL-3.0 code (GPLv3 section 13); the combination is
distributed under AGPL-3.0. Because the app serves its page over a network, anyone who
uses it over a network is entitled to its source: this repository.

## Transmitting

This software keys a radio transmitter. You, the licensed operator, are responsible for
what it puts on the air: check your radio's settings, test into a dummy load first, and
stay within your license privileges.
