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
  linux) OS="linux" ;;
  darwin) OS="darwin" ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

BINARY_NAME="ping-${OS}-${ARCH}"
INSTALL_DIR="${HOME}/.local/bin"
CONFIG_DIR="${HOME}/.config/ping"
LATEST_URL="https://api.github.com/repos/ai-mindset/ping/releases/latest"

echo "Installing ping for ${OS}-${ARCH}..."

# Get latest release info using a better JSON parsing approach
if command -v jq >/dev/null 2>&1; then
  # Use jq if available
  RELEASE_DATA=$(curl -s "$LATEST_URL")
  DOWNLOAD_URL=$(echo "$RELEASE_DATA" | jq -r ".assets[] | select(.name == \"$BINARY_NAME\") | .browser_download_url")
else
  # Fallback method if jq is not available
  RELEASE_DATA=$(curl -s "$LATEST_URL")
  DOWNLOAD_URL=$(echo "$RELEASE_DATA" | grep -o "\"browser_download_url\":\"[^\"]*${BINARY_NAME}[^\"]*\"" | head -1 | sed 's/"browser_download_url":"//g' | sed 's/"//g')
fi

if [ -z "$DOWNLOAD_URL" ]; then
  echo "Error: Could not find binary for ${OS}-${ARCH}"
  echo "Available assets:"
  curl -s "$LATEST_URL" | grep -o "\"name\":\"[^\"]*\"" | sed 's/"name":"//g' | sed 's/"//g'
  exit 1
fi

# Create install directory
mkdir -p "$INSTALL_DIR"
mkdir -p "$CONFIG_DIR"

# Download and install
echo "Downloading from: $DOWNLOAD_URL"
curl -L "$DOWNLOAD_URL" -o "${INSTALL_DIR}/ping"
chmod +x "${INSTALL_DIR}/ping"

echo "✅ ping installed to ${INSTALL_DIR}/ping"
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
