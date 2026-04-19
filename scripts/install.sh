#!/usr/bin/env bash
set -euo pipefail

REPO="ryanycheng/Syrogo"
SERVICE_NAME="syrogo"
INSTALL_ROOT="/opt/syrogo"
BIN_PATH="$INSTALL_ROOT/bin/syrogo"
CONFIG_PATH="$INSTALL_ROOT/config/config.yaml"
DEFAULT_CONFIG_SOURCE="/etc/syrogo/config.yaml"
CONFIG_SOURCE="$DEFAULT_CONFIG_SOURCE"
SYSTEMD_UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
TMP_DIR=""
VERSION=""
ARCHIVE=""
SERVICE_USER="syrogo"
SKIP_HEALTHCHECK=0
FORCE_CONFIG=0
HEALTH_URL="http://127.0.0.1:23234/healthz"

usage() {
  cat <<'EOF'
Usage:
  sudo bash ./scripts/install.sh --archive <path>
  sudo bash ./scripts/install.sh --version <tag>
  curl -fsSL <raw-install-url> | sudo bash -s -- --version <tag>

Options:
  --archive <path>       Local release archive (.tar.gz)
  --version <tag>        Release tag such as v0.1.0
  --config <path>        Local config source path (default: /etc/syrogo/config.yaml)
  --force-config         Overwrite /opt/syrogo/config/config.yaml from --config
  --user <name>          Service user (default: syrogo)
  --install-root <path>  Install root (default: /opt/syrogo)
  --health-url <url>     Health check URL (default: http://127.0.0.1:23234/healthz)
  --skip-healthcheck     Skip final health check
  -h, --help             Show this help

Notes:
  - Local and remote install use the same script entrypoint.
  - The installer keeps an existing installed config by default.
  - If /opt/syrogo/config/config.yaml does not exist yet, --config must point to a local file.
EOF
}

log() {
  printf '[install] %s\n' "$*"
}

fail() {
  printf '[install] %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    fail "please run as root (for example with sudo)"
  fi
}

require_linux_systemd() {
  [ "$(uname -s)" = "Linux" ] || fail "this installer only supports Linux"
  command -v systemctl >/dev/null 2>&1 || fail "systemctl is required"
  [ -d /run/systemd/system ] || fail "systemd is not available on this host"
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --archive)
        [ "$#" -ge 2 ] || fail "missing value for --archive"
        ARCHIVE="$2"
        shift 2
        ;;
      --version)
        [ "$#" -ge 2 ] || fail "missing value for --version"
        VERSION="$2"
        shift 2
        ;;
      --config)
        [ "$#" -ge 2 ] || fail "missing value for --config"
        CONFIG_SOURCE="$2"
        shift 2
        ;;
      --force-config)
        FORCE_CONFIG=1
        shift
        ;;
      --user)
        [ "$#" -ge 2 ] || fail "missing value for --user"
        SERVICE_USER="$2"
        shift 2
        ;;
      --install-root)
        [ "$#" -ge 2 ] || fail "missing value for --install-root"
        INSTALL_ROOT="$2"
        shift 2
        ;;
      --health-url)
        [ "$#" -ge 2 ] || fail "missing value for --health-url"
        HEALTH_URL="$2"
        shift 2
        ;;
      --skip-healthcheck)
        SKIP_HEALTHCHECK=1
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        fail "unknown argument: $1"
        ;;
    esac
  done

  BIN_PATH="$INSTALL_ROOT/bin/syrogo"
  CONFIG_PATH="$INSTALL_ROOT/config/config.yaml"
  SYSTEMD_UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

  if [ -n "$ARCHIVE" ] && [ -n "$VERSION" ]; then
    fail "use either --archive or --version, not both"
  fi
  if [ -z "$ARCHIVE" ] && [ -z "$VERSION" ]; then
    fail "either --archive or --version is required"
  fi
}

validate_config_input() {
  if [ -f "$CONFIG_PATH" ] && [ "$FORCE_CONFIG" -eq 0 ]; then
    log "keeping existing config: $CONFIG_PATH"
    return
  fi

  [ -f "$CONFIG_SOURCE" ] || fail "config file not found: $CONFIG_SOURCE"
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)
      printf 'amd64'
      ;;
    aarch64|arm64)
      printf 'arm64'
      ;;
    *)
      fail "unsupported architecture: $(uname -m)"
      ;;
  esac
}

download_archive() {
  local arch url
  arch="$(detect_arch)"
  TMP_DIR="$(mktemp -d)"
  ARCHIVE="$TMP_DIR/syrogo_${VERSION}_linux_${arch}.tar.gz"
  url="https://github.com/${REPO}/releases/download/${VERSION}/syrogo_${VERSION}_linux_${arch}.tar.gz"
  log "downloading ${url}"
  curl -fL "$url" -o "$ARCHIVE"
}

ensure_service_user() {
  if id "$SERVICE_USER" >/dev/null 2>&1; then
    return
  fi
  useradd --system --home-dir "$INSTALL_ROOT" --shell /usr/sbin/nologin "$SERVICE_USER"
}

extract_binary() {
  local extract_dir binary_source
  [ -f "$ARCHIVE" ] || fail "archive not found: $ARCHIVE"
  [ "${ARCHIVE##*.}" = "gz" ] || fail "archive must be a .tar.gz file"

  if [ -z "$TMP_DIR" ]; then
    TMP_DIR="$(mktemp -d)"
  fi
  extract_dir="$TMP_DIR/extract"
  mkdir -p "$extract_dir"
  tar -xzf "$ARCHIVE" -C "$extract_dir"

  binary_source="$(find "$extract_dir" -type f -name syrogo | head -n 1)"
  [ -n "$binary_source" ] || fail "syrogo binary not found in archive"

  install -d -m 0755 "$INSTALL_ROOT/bin" "$INSTALL_ROOT/config" "$INSTALL_ROOT/logs" "$INSTALL_ROOT/tmp"
  install -m 0755 "$binary_source" "$BIN_PATH"
}

install_or_keep_config() {
  if [ -f "$CONFIG_PATH" ] && [ "$FORCE_CONFIG" -eq 0 ]; then
    log "config unchanged: $CONFIG_PATH"
    return
  fi

  install -m 0644 "$CONFIG_SOURCE" "$CONFIG_PATH"
  log "installed config from $CONFIG_SOURCE"
}

install_unit() {
  cat > "$SYSTEMD_UNIT_PATH" <<EOF
[Unit]
Description=Syrogo AI Gateway
After=network.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
WorkingDirectory=${INSTALL_ROOT}
ExecStart=${INSTALL_ROOT}/bin/syrogo -config ${INSTALL_ROOT}/config/config.yaml
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
}

start_service() {
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME" >/dev/null
  systemctl restart "$SERVICE_NAME"
}

healthcheck() {
  [ "$SKIP_HEALTHCHECK" -eq 1 ] && return
  command -v curl >/dev/null 2>&1 || fail "curl is required for the final health check"
  for _ in 1 2 3 4 5; do
    if curl -fsS "$HEALTH_URL" >/dev/null; then
      log "health check passed: $HEALTH_URL"
      return
    fi
    sleep 1
  done
  fail "health check failed: $HEALTH_URL"
}

main() {
  require_root
  require_linux_systemd
  parse_args "$@"
  validate_config_input
  if [ -n "$VERSION" ]; then
    download_archive
  fi
  ensure_service_user
  extract_binary
  install_or_keep_config
  install_unit
  chown -R "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_ROOT"
  start_service
  healthcheck
  log "installed Syrogo to $INSTALL_ROOT"
  log "config path: $CONFIG_PATH"
}

main "$@"
