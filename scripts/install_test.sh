#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SYROGO_INSTALL_TEST_MODE=1
# shellcheck source=install.sh
source "$SCRIPT_DIR/install.sh"

reset_install_state() {
  INSTALL_ROOT="/opt/syrogo"
  BIN_PATH="$INSTALL_ROOT/bin/syrogo"
  CONFIG_PATH="$INSTALL_ROOT/config/config.yaml"
  DEFAULT_CONFIG_SOURCE="$CONFIG_PATH"
  CONFIG_SOURCE="$DEFAULT_CONFIG_SOURCE"
  SYSTEMD_UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
  VERSION=""
  ARCHIVE=""
  UNINSTALL=0
  PURGE_CONFIG=0
  SKIP_HEALTHCHECK=0
  FORCE_CONFIG=0
  CONFIG_SOURCE_EXPLICIT=0
  BOOTSTRAP=0
  BOOTSTRAP_ADMIN_TOKEN=""
  BOOTSTRAP_CLIENT_TOKEN=""
  CONFIG_INITIALIZED=0
  CONFIG_UPDATED=0
  unset SYROGO_BOOTSTRAP_ADMIN_TOKEN || true
}

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

assert_not_contains() {
  local haystack needle message
  haystack="$1"
  needle="$2"
  message="$3"
  if [[ "$haystack" == *"$needle"* ]]; then
    printf 'FAIL: %s: %q unexpectedly contains %q\n' "$message" "$haystack" "$needle" >&2
    exit 1
  fi
}

assert_fails() {
  local message
  message="$1"
  shift
  if ("$@") >/dev/null 2>&1; then
    printf 'FAIL: %s: command unexpectedly succeeded\n' "$message" >&2
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

test_bootstrap_argument_contract() {
  local fixture
  fixture="$(mktemp -d)"

  reset_install_state
  assert_fails "bootstrap without version" parse_args --bootstrap

  reset_install_state
  SYROGO_BOOTSTRAP_ADMIN_TOKEN="admin-token"
  assert_fails "bootstrap with empty version" parse_args --bootstrap --version ""

  reset_install_state
  SYROGO_BOOTSTRAP_ADMIN_TOKEN="admin-token"
  assert_fails "bootstrap with archive" parse_args --bootstrap --version v1.2.3 --archive "$fixture/release.tar.gz"

  reset_install_state
  SYROGO_BOOTSTRAP_ADMIN_TOKEN="admin-token"
  assert_fails "bootstrap with force config" parse_args --bootstrap --version v1.2.3 --force-config

  reset_install_state
  SYROGO_BOOTSTRAP_ADMIN_TOKEN="admin-token"
  assert_fails "bootstrap with custom config" parse_args --bootstrap --version v1.2.3 --config "$fixture/custom.yaml"

  reset_install_state
  assert_fails "bootstrap without admin token" parse_args --bootstrap --version v1.2.3

  reset_install_state
  SYROGO_BOOTSTRAP_ADMIN_TOKEN="unsafe token"
  assert_fails "bootstrap with unsafe admin token" parse_args --bootstrap --version v1.2.3

  reset_install_state
  INSTALL_ROOT="$fixture/root"
  mkdir -p "$INSTALL_ROOT/config"
  printf 'existing config\n' > "$INSTALL_ROOT/config/config.yaml"
  SYROGO_BOOTSTRAP_ADMIN_TOKEN="admin_token-safe"
  assert_fails "bootstrap with existing config" parse_args --bootstrap --version v1.2.3 --install-root "$INSTALL_ROOT"

  reset_install_state
  INSTALL_ROOT="$fixture/new-root"
  SYROGO_BOOTSTRAP_ADMIN_TOKEN="admin_token-safe"
  parse_args --bootstrap --version v1.2.3 --install-root "$INSTALL_ROOT"
  assert_eq "1" "$BOOTSTRAP" "bootstrap mode enabled"
  assert_eq "admin_token-safe" "$BOOTSTRAP_ADMIN_TOKEN" "bootstrap admin token captured"
  assert_eq "$INSTALL_ROOT/config/config.yaml" "$CONFIG_SOURCE" "bootstrap config follows install root"
  assert_eq "https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/tags/v1.2.3/configs/config.console-bootstrap.yaml" "$(config_init_url)" "bootstrap template URL"

  rm -rf "$fixture"
}

test_bootstrap_config_rendering() {
  local fixture template output contents mode admin_token
  fixture="$(mktemp -d)"
  template="$SCRIPT_DIR/../configs/config.console-bootstrap.yaml"
  admin_token="Admin_safe-123"

  reset_install_state
  BOOTSTRAP=1
  VERSION="v1.2.3"
  BOOTSTRAP_ADMIN_TOKEN="$admin_token"
  INSTALL_ROOT="$fixture/root"
  CONFIG_PATH="$INSTALL_ROOT/config/config.yaml"
  DEFAULT_CONFIG_SOURCE="$CONFIG_PATH"
  CONFIG_SOURCE="$DEFAULT_CONFIG_SOURCE"
  curl_download() {
    assert_eq "https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/tags/v1.2.3/configs/config.console-bootstrap.yaml" "$1" "downloaded bootstrap template URL"
    cp "$template" "$2"
  }
  generate_bootstrap_client_token() {
    printf '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n'
  }

  output="$(download_bootstrap_config 2>&1 >/dev/null)"
  contents="$(<"$CONFIG_PATH")"
  mode="$(stat -c '%a' "$CONFIG_PATH")"
  assert_contains "$output" "config.console-bootstrap.yaml" "bootstrap download diagnostic"
  assert_contains "$contents" "token: \"$admin_token\"" "admin token replacement"
  assert_contains "$contents" 'token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"' "client token replacement"
  assert_not_contains "$contents" "__SYROGO_CONSOLE_" "bootstrap placeholders cleared"
  assert_eq "600" "$mode" "bootstrap config mode"

  printf 'existing config\n' > "$CONFIG_PATH"
  if (download_bootstrap_config) >/dev/null 2>&1; then
    printf 'FAIL: bootstrap config overwrote existing config\n' >&2
    exit 1
  fi
  assert_eq "existing config" "$(<"$CONFIG_PATH")" "bootstrap existing config preservation"

  rm -rf "$fixture"
}

test_verify_release_archive
test_config_mode_and_preservation
test_version_receipts
test_bootstrap_argument_contract
test_bootstrap_config_rendering
printf 'installer tests passed\n'
