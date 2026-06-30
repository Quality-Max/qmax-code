#!/bin/bash
# QualityMax Code Agent Installer
# Usage: curl -sL https://qualitymax.io/static/install-qmax-code.txt | bash

set -e

REPO="Quality-Max/qmax-code-releases"
BINARY="qmax-code"

echo "QualityMax Code Agent Installer"
echo "================================"
echo ""

# Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
    *)
        echo "Error: Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

case "$OS" in
    darwin|linux) ;;
    *)
        echo "Error: Unsupported OS: $OS"
        echo "For Windows, download from: https://github.com/$REPO/releases/latest"
        exit 1
        ;;
esac

BINARY_NAME="${BINARY}-${OS}-${ARCH}"
ARCHIVE_NAME="${BINARY_NAME}.tar.gz"
echo "Detected: ${OS}/${ARCH}"

# Install directory
INSTALL_DIR="${HOME}/.qmax-code"
mkdir -p "$INSTALL_DIR"

# Determine version
if [ -n "$QMAX_CODE_VERSION" ]; then
    VERSION="$QMAX_CODE_VERSION"
    DOWNLOAD_URL="https://github.com/$REPO/releases/download/${VERSION}/${ARCHIVE_NAME}"
    echo "Installing version: $VERSION"
else
    DOWNLOAD_URL="https://github.com/$REPO/releases/latest/download/${ARCHIVE_NAME}"
    echo "Installing latest version..."
fi

# Download
TMPFILE="$(mktemp /tmp/qmax-code-XXXXXX.tar.gz)"
trap 'rm -f "$TMPFILE"' EXIT

echo "Downloading ${ARCHIVE_NAME}..."
if command -v curl &> /dev/null; then
    HTTP_CODE=$(curl -sL -w "%{http_code}" -o "$TMPFILE" "$DOWNLOAD_URL")
    if [ "$HTTP_CODE" -ne 200 ]; then
        echo "Error: Download failed (HTTP $HTTP_CODE)"
        echo "Check releases: https://github.com/$REPO/releases"
        exit 1
    fi
elif command -v wget &> /dev/null; then
    if ! wget -q -O "$TMPFILE" "$DOWNLOAD_URL"; then
        echo "Error: Download failed"
        exit 1
    fi
else
    echo "Error: curl or wget required"
    exit 1
fi

echo "Extracting..."
tar -xzf "$TMPFILE" -C "$INSTALL_DIR" "$BINARY_NAME"
mv "$INSTALL_DIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY"

chmod +x "$INSTALL_DIR/$BINARY"
echo "Installed to: $INSTALL_DIR/$BINARY"

# Symlink to /usr/local/bin
RUN_CMD="$INSTALL_DIR/$BINARY"
if [ -w /usr/local/bin ]; then
    ln -sf "$INSTALL_DIR/$BINARY" /usr/local/bin/$BINARY
    echo "Linked: /usr/local/bin/$BINARY"
    RUN_CMD="$BINARY"
else
    echo ""
    echo "To make '$BINARY' available globally:"
    echo "  sudo ln -sf $INSTALL_DIR/$BINARY /usr/local/bin/$BINARY"
    echo ""
    echo "Or add to PATH:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi

echo ""

INSTALLED_VERSION="$("$INSTALL_DIR/$BINARY" --version 2>/dev/null | awk '{print $NF}' || true)"
if [ -n "$INSTALLED_VERSION" ]; then
    echo "Installed version: $INSTALLED_VERSION"
else
    echo "Installed version: unknown"
fi
if [ -n "$VERSION" ]; then
    echo "Release: https://github.com/$REPO/releases/tag/$VERSION"
else
    echo "Release: https://github.com/$REPO/releases/latest"
fi

echo ""
echo "qmax CLI is not required. qmax-code works standalone with the QualityMax API."
echo ""
echo "Next steps:"
echo "  1. Start qmax-code and finish interactive setup:"
echo "     $RUN_CMD"
echo "  2. Optional: connect QualityMax now:"
echo "     $RUN_CMD login"
echo "  3. Optional: attach Codex for QualityMax mobile runs:"
echo "     $RUN_CMD codex connect"
echo ""
echo "Auth options:"
echo "  - Browser login: $RUN_CMD login"
echo "  - API key:       $RUN_CMD login --api-key qm-YOUR-API-KEY"
echo "  - Anthropic:     set ANTHROPIC_API_KEY or save it when prompted"
echo ""
echo "Docs: https://qualitymax.io/docs/qmax-code"
