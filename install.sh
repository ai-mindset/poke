#!/bin/bash
set -e

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case $ARCH in
  x86_64) ARCH="x86_64" ;;
  arm64|aarch64) ARCH="aarch64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case $OS in
  linux) EXT="" ;;
  darwin) EXT="" ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

BINARY_NAME="poke-${OS}-${ARCH}${EXT}"
INSTALL_DIR="${HOME}/.local/bin"
CONFIG_DIR="${HOME}/.config/poke"
LATEST_URL="https://api.github.com/repos/ai-mindset/poke/releases/latest"

echo "Installing poke for ${OS}-${ARCH}..."

# Get latest release info
DOWNLOAD_URL=$(curl -s "$LATEST_URL" | grep -o "https://.*/${BINARY_NAME}" | head -1)

if [ -z "$DOWNLOAD_URL" ]; then
  echo "Error: Could not find binary for ${OS}-${ARCH}"
  exit 1
fi

# Create install directory
mkdir -p "$INSTALL_DIR"
mkdir -p "$CONFIG_DIR"

# Download and install
echo "Downloading from: $DOWNLOAD_URL"
curl -L "$DOWNLOAD_URL" -o "${INSTALL_DIR}/poke"
chmod +x "${INSTALL_DIR}/poke"

echo "✅ poke installed to ${INSTALL_DIR}/poke"
echo ""
echo "Add to PATH if needed:"
echo "export PATH=\"\$HOME/.local/bin:\$PATH\""
echo ""
echo "Create config file with your GitHub settings:"
echo "mkdir -p \"$CONFIG_DIR\""
echo "cat > \"$CONFIG_DIR/.env\" << EOF
GITHUB_TOKEN=ghp_your_token
GITHUB_USERNAME=your_username
WORK_ORGS=Your-Organization
WORK_TEAMS=team1,team2,team3
EOF"
