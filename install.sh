#!/bin/bash
set -euo pipefail

: "${HOME:?HOME must be set}"

for command_name in curl install mktemp; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "Error: Required command not found: ${command_name}" >&2
    exit 1
  fi
done

os=$(uname -s | tr '[:upper:]' '[:lower:]')
architecture=$(uname -m)

case "$architecture" in
  x86_64) architecture="x86_64" ;;
  arm64 | aarch64) architecture="aarch64" ;;
  *)
    echo "Error: Unsupported architecture: ${architecture}" >&2
    exit 1
    ;;
esac

case "$os" in
  linux | darwin) ;;
  *)
    echo "Error: Unsupported operating system: ${os}" >&2
    exit 1
    ;;
esac

binary_name="poke-${os}-${architecture}"
install_dir="${HOME}/.local/bin"
release_base_url="https://github.com/ai-mindset/poke/releases/latest/download"
temporary_dir=$(mktemp -d)
temporary_install_path=""

cleanup() {
  if [[ -n "$temporary_install_path" && -f "$temporary_install_path" ]]; then
    rm -f "$temporary_install_path"
  fi
  if [[ -d "$temporary_dir" ]]; then
    rm -rf "$temporary_dir"
  fi
}
trap cleanup EXIT

binary_path="${temporary_dir}/${binary_name}"
checksums_path="${temporary_dir}/SHA256SUMS"
curl_options=(--fail --silent --show-error --location --proto '=https' --tlsv1.2)

echo "Installing Poke for ${os}-${architecture}..."
curl "${curl_options[@]}" --output "$binary_path" \
  "${release_base_url}/${binary_name}"
curl "${curl_options[@]}" --output "$checksums_path" \
  "${release_base_url}/SHA256SUMS"

expected_hash=$(
  awk -v asset="$binary_name" \
    '$2 == asset || $2 == "*" asset { print $1; exit }' \
    "$checksums_path"
)

if ! printf '%s\n' "$expected_hash" | grep -Eq '^[0-9a-fA-F]{64}$'; then
  echo "Error: SHA256SUMS has no valid entry for ${binary_name}" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual_hash=$(sha256sum "$binary_path" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  actual_hash=$(shasum -a 256 "$binary_path" | awk '{ print $1 }')
else
  echo "Error: sha256sum or shasum is required to verify the download" >&2
  exit 1
fi

expected_hash=$(printf '%s' "$expected_hash" | tr '[:upper:]' '[:lower:]')
actual_hash=$(printf '%s' "$actual_hash" | tr '[:upper:]' '[:lower:]')
if [[ "$actual_hash" != "$expected_hash" ]]; then
  echo "Error: Checksum verification failed for ${binary_name}" >&2
  exit 1
fi

chmod +x "$binary_path"
if ! "$binary_path" --help >/dev/null; then
  echo "Error: The downloaded executable failed its smoke test" >&2
  exit 1
fi

mkdir -p "$install_dir"
temporary_install_path="${install_dir}/.poke-install-$$"
install -m 0755 "$binary_path" "$temporary_install_path"
mv -f "$temporary_install_path" "${install_dir}/poke"
temporary_install_path=""

echo "Poke installed to ${install_dir}/poke"
if [[ ":${PATH}:" != *":${install_dir}:"* ]]; then
  echo "Add this line to your shell profile, then start a new terminal:"
  echo 'export PATH="$HOME/.local/bin:$PATH"'
fi
echo "Run 'poke --help' to get started."
