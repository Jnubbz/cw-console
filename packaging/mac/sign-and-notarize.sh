#!/bin/bash
# Optional: sign and notarize CW Decoder with YOUR Apple Developer ID, so it opens like
# any downloaded app (no security prompt) and could be shared with other hams.
#
# One-time setup (on this Mac, with Xcode or the Command Line Tools installed):
#   1. Have a "Developer ID Application" certificate in your keychain
#      (Xcode > Settings > Accounts > Manage Certificates > + > Developer ID Application).
#   2. Save notarization credentials (use an app-specific password from appleid.apple.com):
#        xcrun notarytool store-credentials cwdecoder --apple-id YOU@EXAMPLE.COM --team-id TEAMID
#
# Then:  ./sign-and-notarize.sh "CW Decoder.app"
set -euo pipefail
APP="${1:-CW Decoder.app}"
PROFILE="${NOTARY_PROFILE:-cwdecoder}"
IDENTITY=$(security find-identity -v -p codesigning | grep "Developer ID Application" | head -1 | sed -E 's/.*"(.*)"/\1/')
if [ -z "$IDENTITY" ]; then
  echo "No 'Developer ID Application' certificate found in your keychain (see the setup notes at the top)."
  exit 1
fi
echo "Signing with: $IDENTITY"
xattr -cr "$APP"
codesign --force --options runtime --timestamp --sign "$IDENTITY" "$APP/Contents/MacOS/cw-decoder"
codesign --force --options runtime --timestamp --sign "$IDENTITY" "$APP"
codesign --verify --deep --strict --verbose=2 "$APP"
ZIP="$(mktemp -d)/CW-Decoder-notarize.zip"
ditto -c -k --keepParent "$APP" "$ZIP"
echo "Submitting to Apple for notarization (usually a few minutes)..."
xcrun notarytool submit "$ZIP" --keychain-profile "$PROFILE" --wait
xcrun stapler staple "$APP"
spctl --assess --type execute --verbose "$APP"
echo "Done: $APP is signed and notarized."
