#!/bin/sh
# Re-records docs/demo.gif and the screenshots from a real teveus session.
# Needs: a logged-in `claude` CLI, ffmpeg, and the JetBrains Mono font.
set -eu
cd "$(dirname "$0")/.."

# A fresh copy of the demo project (Claude edits it during the recording).
rm -rf "$HOME/demo-app"
cp -r docs/demo-app "$HOME/demo-app"
(cd "$HOME/demo-app" && git init -q && git add -A && git -c user.email=demo@example.com -c user.name=demo commit -qm init)

# A clean config so no personal settings or keys appear.
CFG="$(mktemp -d)"
echo '{"onboarded":true,"engine":"claude","level":"standard"}' > "$CFG/settings.json"
FRAMES="$(mktemp -d)"

TEVEUS_CONFIG="$CFG" TEVEUS_CAPTURE="$FRAMES" TEVEUS_CAPTURE_PROJECT="$HOME/demo-app" \
  go test ./internal/ui -run '^TestCapture$' -count=1 -timeout 5m
(cd docs/screencast && go run . -scale 2 -in "$FRAMES/frames.json" -out ..)

rm -rf "$CFG" "$FRAMES" "$HOME/demo-app"
