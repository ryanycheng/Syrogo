#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SYROGO_INSTALL_TEST_MODE=1
# shellcheck source=install.sh
source "$SCRIPT_DIR/install.sh"

assert_eq() {
  local want got message
  want="$1"
  got="$2"
  message="$3"
  if [ "$got" != "$want" ]; then
    printf 'FAIL: %s: got %q, want %q\n' "$message" "$got" "$want" >&2
    exit 1
  fi
}

assert_contains() {
  local haystack needle message
  haystack="$1"
  needle="$2"
  message="$3"
  if [[ "$haystack" != *"$needle"* ]]; then
    printf 'FAIL: %s: %q does not contain %q\n' "$message" "$haystack" "$needle" >&2
    exit 1
  fi
}

test_verify_release_archive() {
  local fixture hash output
  fixture="$(mktemp -d)"
  TMP_DIR="$fixture"
  VERSION="v1.2.3"
  ARCHIVE="$fixture/syrogo_${VERSION}_linux_amd64.tar.gz"
  printf 'release archive' > "$ARCHIVE"
  hash="$(sha256sum "$ARCHIVE")"
  hash="${hash%% *}"
  curl_download() {
    printf '%s  %s\n' "$hash" "$(basename "$ARCHIVE")" > "$2"
  }

  output="$(verify_release_archive)"
  assert_contains "$output" "verified release checksum" "release archive verification"

  curl_download() {
    printf '%064d  %s\n' 0 "$(basename "$ARCHIVE")" > "$2"
  }
  if (verify_release_archive) >/dev/null 2>&1; then
    printf 'FAIL: mismatched release checksum was accepted\n' >&2
    exit 1
  fi
  rm -rf "$fixture"
}

test_config_mode_and_preservation() {
  local fixture mode contents
  fixture="$(mktemp -d)"
  INSTALL_ROOT="$fixture/root"
  CONFIG_PATH="$INSTALL_ROOT/config/config.yaml"
  CONFIG_SOURCE="$fixture/source.yaml"
  mkdir -p "$(dirname "$CONFIG_PATH")"
  printf 'new config\n' > "$CONFIG_SOURCE"
  FORCE_CONFIG=0

  install_or_keep_config >/dev/null
  mode="$(stat -c '%a' "$CONFIG_PATH")"
  assert_eq "600" "$mode" "new config mode"

  printf 'existing config\n' > "$CONFIG_PATH"
  chmod 0644 "$CONFIG_PATH"
  install_or_keep_config >/dev/null
  contents="$(<"$CONFIG_PATH")"
  mode="$(stat -c '%a' "$CONFIG_PATH")"
  assert_eq "existing config" "$contents" "existing config preservation"
  assert_eq "644" "$mode" "existing config mode preservation"
  rm -rf "$fixture"
}

test_version_receipts() {
  local fixture output
  fixture="$(mktemp -d)"
  INSTALL_ROOT="$fixture"

  VERSION="v1.2.3"
  write_version_receipt >/dev/null
  assert_eq "v1.2.3" "$(<"$INSTALL_ROOT/VERSION")" "release version receipt"

  VERSION=""
  output="$(write_version_receipt)"
  assert_eq "local-archive" "$(<"$INSTALL_ROOT/VERSION")" "local archive receipt"
  assert_contains "$output" "release checksum was not verified" "local archive verification status"
  rm -rf "$fixture"
}

test_verify_release_archive
test_config_mode_and_preservation
test_version_receipts
printf 'installer tests passed\n'
