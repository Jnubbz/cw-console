#!/bin/bash
# Builds the Windows and Mac (Apple Silicon) packages into dist/.
# Needs Go 1.24+. No cgo: cross-builds from Linux, Mac or Windows (Git Bash).
set -euo pipefail
cd "$(dirname "$0")/.."
VER="${VERSION:-dev}"
OUT=dist
rm -rf "$OUT"; mkdir -p "$OUT"
MODEL=third_party/deepcw-engine

model() { # the DeepCW model next to the app
  mkdir -p "$1"
  cp "$MODEL/model.onnx" "$MODEL/model.onnx.json" "$MODEL/LICENSE" "$1/"
}

# Windows: a native window (cmd/cw-decoder) + a command-line file decoder
W="$OUT/CW-Decoder-Windows"
mkdir -p "$W"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui -X main.appVersion=$VER" -o "$W/CW-Decoder.exe" ./cmd/cw-decoder
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$W/cwdecode-file.exe" ./cmd/cwdecode-file
model "$W/deepcw"
cp packaging/windows/README.txt "$W/README.txt"; cp LICENSE "$W/LICENSE.txt"; cp NOTICE.md "$W/"

# Mac: a background app with the page in the browser (cmd/cwmac)
M="$OUT/CW-Decoder-Mac"
APP="$M/CW Decoder.app/Contents"
mkdir -p "$APP/MacOS" "$APP/Resources"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.appVersion=$VER" -o "$APP/MacOS/cw-decoder" ./cmd/cwmac
cp packaging/mac/Info.plist "$APP/Info.plist"
if [[ "$VER" =~ ^[0-9]+\.[0-9]+\.([0-9]+) ]]; then # Finder's version: the release's
  sed -i.bak -e "s#<key>CFBundleShortVersionString</key><string>[^<]*</string>#<key>CFBundleShortVersionString</key><string>${VER%%-*}</string>#" \
    -e "s#<key>CFBundleVersion</key><string>[^<]*</string>#<key>CFBundleVersion</key><string>${BASH_REMATCH[1]}</string>#" "$APP/Info.plist"
  rm -f "$APP/Info.plist.bak"
fi
model "$APP/Resources/deepcw"
cp packaging/mac/README-Mac.txt packaging/mac/sign-and-notarize.sh "$M/"; cp LICENSE "$M/LICENSE.txt"; cp NOTICE.md "$M/"

(cd "$OUT" && for d in CW-Decoder-Windows CW-Decoder-Mac; do
  if command -v zip >/dev/null; then zip -qry "$d.zip" "$d"; else tar czf "$d.tar.gz" "$d"; fi
done)
ls -la "$OUT"
