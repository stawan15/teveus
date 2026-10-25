#!/bin/sh
# Re-records docs/demo.gif and the screenshots by driving the real UI through
# a scripted session. It makes no network requests and needs no AI key.
# Needs: ffmpeg and the JetBrains Mono font.
set -eu
cd "$(dirname "$0")/.."

# A fresh copy of the demo project, with a clean git history.
rm -rf "$HOME/demo-app"
cp -r docs/demo-app "$HOME/demo-app"
(cd "$HOME/demo-app" && git init -q && git add -A && git -c user.email=demo@example.com -c user.name=demo commit -qm init)

# A clean config so no personal settings or keys appear.
CFG="$(mktemp -d)"
echo '{"onboarded":true,"engine":"claude","level":"standard","claudeCodeConfirmed":true}' > "$CFG/settings.json"
FRAMES="$(mktemp -d)"

TEVEUS_CONFIG="$CFG" TEVEUS_CAPTURE="$FRAMES" TEVEUS_CAPTURE_PROJECT="$HOME/demo-app" \
  go test ./internal/ui -run '^TestCaptureDemo$' -count=1 -timeout 5m
(cd docs/screencast && go run . -scale 2 -in "$FRAMES/frames.json" -out ..)

rm -rf "$CFG" "$FRAMES" "$HOME/demo-app"
