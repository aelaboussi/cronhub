#!/bin/sh
# cronhub installer.
#
#   curl -fsSL https://raw.githubusercontent.com/aelaboussi/cronhub/main/install.sh | sh
#
# Downloads the latest released cronhub binary for your OS and CPU and installs
# it. Set CRONHUB_INSTALL_DIR to choose where it goes (default: /usr/local/bin,
# falling back to ~/.local/bin if that isn't writable). Set CRONHUB_VERSION to
# install a specific version instead of the latest.

set -eu

REPO="aelaboussi/cronhub"

say()  { printf '%s\n' "$*"; }
err()  { printf 'error: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- detect OS -------------------------------------------------------------
os="$(uname -s)"
case "$os" in
  Linux)  goos="linux" ;;
  Darwin) goos="darwin" ;;
  *)      err "unsupported OS: $os (this installer covers Linux and macOS; on Windows download the .exe from the releases page)" ;;
esac

# --- detect CPU ------------------------------------------------------------
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) goarch="amd64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) err "unsupported CPU: $arch" ;;
esac

asset="cronhub-${goos}-${goarch}"

# --- figure out which version ----------------------------------------------
if [ "${CRONHUB_VERSION:-}" != "" ]; then
  version="$CRONHUB_VERSION"
else
  # Ask GitHub for the latest release tag.
  api="https://api.github.com/repos/${REPO}/releases/latest"
  if have curl; then
    version="$(curl -fsSL "$api" | grep '"tag_name":' | head -1 | cut -d'"' -f4)"
  elif have wget; then
    version="$(wget -qO- "$api" | grep '"tag_name":' | head -1 | cut -d'"' -f4)"
  else
    err "need curl or wget installed"
  fi
  [ -n "$version" ] || err "could not determine the latest version (has a release been published yet?)"
fi

url="https://github.com/${REPO}/releases/download/${version}/${asset}"
say "Installing cronhub ${version} for ${goos}/${goarch}"

# --- download --------------------------------------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
bin="${tmp}/cronhub"

if have curl; then
  curl -fSL "$url" -o "$bin" || err "download failed: $url"
else
  wget -q "$url" -O "$bin" || err "download failed: $url"
fi
chmod +x "$bin"

# --- choose an install directory -------------------------------------------
dir="${CRONHUB_INSTALL_DIR:-/usr/local/bin}"
if [ ! -d "$dir" ] || [ ! -w "$dir" ]; then
  # try with sudo if available and the dir is the default system one
  if [ "$dir" = "/usr/local/bin" ] && have sudo; then
    say "Installing to $dir (needs sudo)"
    sudo mkdir -p "$dir"
    sudo mv "$bin" "$dir/cronhub"
    installed="$dir/cronhub"
  else
    # fall back to a per-user location that needs no admin rights
    dir="${HOME}/.local/bin"
    mkdir -p "$dir"
    mv "$bin" "$dir/cronhub"
    installed="$dir/cronhub"
  fi
else
  mv "$bin" "$dir/cronhub"
  installed="$dir/cronhub"
fi

say ""
say "Installed to $installed"

# --- PATH hint -------------------------------------------------------------
case ":$PATH:" in
  *":$dir:"*) : ;;  # already on PATH
  *)
    say ""
    say "Note: $dir is not on your PATH. Add this to your shell profile:"
    say "    export PATH=\"\$PATH:$dir\""
    ;;
esac

say ""
say "Run 'cronhub init' to get started, or 'cronhub --version' to check the install."
