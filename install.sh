#!/bin/sh
# teveus installer
#
#   curl -fsSL https://raw.githubusercontent.com/stawan15/teveus/main/install.sh | sh
#
# Options (environment variables):
#   TEVEUS_INSTALL_DIR   where to put the binary      (default: ~/.local/bin)
#   TEVEUS_VERSION       a release tag, e.g. v0.1.0   (default: latest)
#   TEVEUS_BASE_URL      download location override   (for testing mirrors)

set -eu

REPO="stawan15/teveus"
INSTALL_DIR="${TEVEUS_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${TEVEUS_VERSION:-latest}"

if [ -t 1 ]; then
  BOLD="$(printf '\033[1m')"; DIM="$(printf '\033[2m')"; RED="$(printf '\033[31m')"
  GREEN="$(printf '\033[32m')"; ACCENT="$(printf '\033[38;5;209m')"; RESET="$(printf '\033[0m')"
else
  BOLD=""; DIM=""; RED=""; GREEN=""; ACCENT=""; RESET=""
fi

say()  { printf '%s\n' "$*"; }
step() { printf '%s›%s %s\n' "$ACCENT" "$RESET" "$*"; }
fail() { printf '%serror:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

# --- platform -------------------------------------------------------------
case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux)  OS=linux ;;
  MINGW*|MSYS*|CYGWIN*) fail "on Windows, download teveus_windows_amd64.zip from https://github.com/$REPO/releases" ;;
  *) fail "unsupported system: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) fail "unsupported CPU: $(uname -m)" ;;
esac

# --- download -------------------------------------------------------------
if [ -n "${TEVEUS_BASE_URL:-}" ]; then
  BASE="$TEVEUS_BASE_URL"
elif [ "$VERSION" = "latest" ]; then
  BASE="https://github.com/$REPO/releases/latest/download"
else
  BASE="https://github.com/$REPO/releases/download/$VERSION"
fi

fetch() { # url dest
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$1" -O "$2"
  else
    fail "need curl or wget"
  fi
}

ARCHIVE="teveus_${OS}_${ARCH}.tar.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

say ""
say "${BOLD}${ACCENT}✦ teveus${RESET} ${DIM}installer${RESET}"
step "downloading $ARCHIVE ${DIM}($VERSION)${RESET}"
fetch "$BASE/$ARCHIVE" "$TMP/$ARCHIVE" || fail "download failed: $BASE/$ARCHIVE"
fetch "$BASE/checksums.txt" "$TMP/checksums.txt" || fail "download failed: $BASE/checksums.txt"

# --- verify ---------------------------------------------------------------
step "verifying checksum"
EXPECTED="$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | cut -d' ' -f1)"
[ -n "$EXPECTED" ] || fail "$ARCHIVE is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "$TMP/$ARCHIVE" | cut -d' ' -f1)"
else
  ACTUAL="$(shasum -a 256 "$TMP/$ARCHIVE" | cut -d' ' -f1)"
fi
[ "$EXPECTED" = "$ACTUAL" ] || fail "checksum mismatch for $ARCHIVE (expected $EXPECTED, got $ACTUAL)"

# --- install --------------------------------------------------------------
tar -xzf "$TMP/$ARCHIVE" -C "$TMP" teveus
mkdir -p "$INSTALL_DIR"
mv "$TMP/teveus" "$INSTALL_DIR/teveus"
chmod 755 "$INSTALL_DIR/teveus"
INSTALLED="$("$INSTALL_DIR/teveus" -version 2>/dev/null || echo teveus)"
step "installed ${BOLD}$INSTALLED${RESET} to $INSTALL_DIR/teveus"

# --- next steps -----------------------------------------------------------
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    SHELL_RC="~/.profile"
    case "${SHELL:-}" in
      */zsh)  SHELL_RC="~/.zshrc" ;;
      */bash) SHELL_RC="~/.bashrc" ;;
      */fish) SHELL_RC="~/.config/fish/config.fish" ;;
    esac
    say ""
    say "${BOLD}Add teveus to your PATH${RESET} (then open a new terminal):"
    say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> $SHELL_RC"
    ;;
esac

say ""
say "${GREEN}Done.${RESET} Run ${BOLD}teveus${RESET} inside any project folder."
if command -v claude >/dev/null 2>&1; then
  say "${DIM}Claude Code found: connect it inside teveus with /login.${RESET}"
else
  say "${DIM}For your Claude subscription, install Claude Code (https://claude.com/claude-code);"
  say "or use /login inside teveus for OpenAI, Gemini, OpenRouter and other API keys.${RESET}"
fi
say ""
