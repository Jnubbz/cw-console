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
W="$OUT/CW-Console-Windows"
mkdir -p "$W"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui -X main.appVersion=$VER" -o "$W/CW-Console.exe" ./cmd/cw-decoder
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$W/cwdecode-file.exe" ./cmd/cwdecode-file
model "$W/deepcw"
cp packaging/windows/README.txt "$W/README.txt"; cp LICENSE "$W/LICENSE.txt"; cp NOTICE.md "$W/"

# Mac: CW Console.app, a native window (packaging/mac/shell, Swift: needs a Mac) around the
# engine (cmd/cwmac). Built anywhere else, the engine is the app and shows its page in the
# browser instead.
M="$OUT/CW-Console-Mac"
BUNDLE="$M/CW Console.app"
APP="$BUNDLE/Contents"
mkdir -p "$APP/MacOS" "$APP/Resources"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.appVersion=$VER" -o "$APP/MacOS/cw-decoder" ./cmd/cwmac
cp packaging/mac/Info.plist "$APP/Info.plist"
if [[ "$VER" =~ ^[0-9]+\.[0-9]+\.([0-9]+) ]]; then # Finder's version: the release's
  sed -i.bak -e "s#<key>CFBundleShortVersionString</key><string>[^<]*</string>#<key>CFBundleShortVersionString</key><string>${VER%%-*}</string>#" \
    -e "s#<key>CFBundleVersion</key><string>[^<]*</string>#<key>CFBundleVersion</key><string>${BASH_REMATCH[1]}</string>#" "$APP/Info.plist"
  rm -f "$APP/Info.plist.bak"
fi
if command -v swiftc >/dev/null && [ "$(uname)" = Darwin ]; then
  swiftc -O -target arm64-apple-macos12 -framework Cocoa -framework WebKit \
    -o "$APP/MacOS/CW Console" packaging/mac/shell/main.swift
else
  echo "build.sh: no Swift here: the Mac app opens in the browser (build on a Mac for the window)" >&2
  sed -i.bak -e "s#<key>CFBundleExecutable</key><string>[^<]*</string>#<key>CFBundleExecutable</key><string>cw-decoder</string><key>LSUIElement</key><true/>#" "$APP/Info.plist"
  rm -f "$APP/Info.plist.bak"
fi
model "$APP/Resources/deepcw"
if command -v codesign >/dev/null; then
  codesign --force --deep --sign - "$BUNDLE" # ad hoc: seals the bundle (not Apple-notarized)
fi
cp packaging/mac/README-Mac.txt packaging/mac/sign-and-notarize.sh "$M/"; cp LICENSE "$M/LICENSE.txt"; cp NOTICE.md "$M/"

(cd "$OUT" && for d in CW-Console-Windows CW-Console-Mac; do
  if command -v zip >/dev/null; then zip -qry "$d.zip" "$d"; else tar czf "$d.tar.gz" "$d"; fi
done)
ls -la "$OUT"
