#!/usr/bin/env bash
set -euo pipefail

REPO="ryanycheng/Syrogo"
SERVICE_NAME="syrogo"
INSTALL_ROOT="/opt/syrogo"
BIN_PATH="$INSTALL_ROOT/bin/syrogo"
SYMLINK_PATH="/usr/local/bin/syrogo"
CONFIG_PATH="$INSTALL_ROOT/config/config.yaml"
DEFAULT_CONFIG_SOURCE="$INSTALL_ROOT/config/config.yaml"
CONFIG_SOURCE="$DEFAULT_CONFIG_SOURCE"
SYSTEMD_UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
TMP_DIR=""
VERSION=""
ARCHIVE=""
UNINSTALL=0
PURGE_CONFIG=0
SERVICE_USER="syrogo"
SKIP_HEALTHCHECK=0
FORCE_CONFIG=0
CONFIG_SOURCE_EXPLICIT=0
BOOTSTRAP=0
BOOTSTRAP_ADMIN_TOKEN=""
BOOTSTRAP_CLIENT_TOKEN=""
CONFIG_INITIALIZED=0
CONFIG_UPDATED=0
HEALTH_URL="http://127.0.0.1:23234/healthz"
DOWNLOAD_PROXY="${SYROGO_INSTALL_PROXY:-}"
CURL_RETRY="${SYROGO_INSTALL_RETRY:-5}"
CURL_CONNECT_TIMEOUT="${SYROGO_INSTALL_CONNECT_TIMEOUT:-60}"
CURL_MAX_TIME="${SYROGO_INSTALL_MAX_TIME:-600}"
CURL_LOW_SPEED_LIMIT="${SYROGO_INSTALL_LOW_SPEED_LIMIT:-1}"
CURL_LOW_SPEED_TIME="${SYROGO_INSTALL_LOW_SPEED_TIME:-60}"

usage() {
  cat <<'EOF'
Usage:
  sudo bash ./scripts/install.sh
  sudo bash ./scripts/install.sh --archive <path>
  sudo bash ./scripts/install.sh --version <tag>
  sudo env SYROGO_BOOTSTRAP_ADMIN_TOKEN=<token> bash ./scripts/install.sh --bootstrap --version <tag>
  sudo bash ./scripts/install.sh --uninstall
  sudo bash ./scripts/install.sh --uninstall --purge-config
  curl -fsSL <raw-install-url> | sudo bash -s --
  curl -fsSL <raw-install-url> | sudo bash -s -- --version <tag>

Options:
  --archive <path>       Local release archive (.tar.gz)
  --version <tag>        Release tag such as v0.1.0
  --bootstrap            Initialize a new Console bootstrap config (requires --version and SYROGO_BOOTSTRAP_ADMIN_TOKEN)
  --uninstall            Remove installed service and files under /opt/syrogo
  --purge-config         Kept for compatibility; uninstall already removes the default /opt config
  --config <path>        Local config source path (default: /opt/syrogo/config/config.yaml)
  --force-config         Overwrite /opt/syrogo/config/config.yaml from --config
  --user <name>          Service user (default: syrogo)
  --install-root <path>  Install root (default: /opt/syrogo)
  --symlink <path>       Command symlink path (default: /usr/local/bin/syrogo)
  --proxy <url>          Proxy for installer downloads, e.g. http://127.0.0.1:7890
  --health-url <url>     Health check URL (default: http://127.0.0.1:23234/healthz)
  --skip-healthcheck     Skip final health check
  -h, --help             Show this help

Notes:
  - Local and remote install use the same script entrypoint.
  - Without --version or --archive, the installer uses the latest GitHub release.
  - Downloads honor --proxy or SYROGO_INSTALL_PROXY; curl retry and timeout knobs use SYROGO_INSTALL_* env vars.
  - On first install, if /opt/syrogo/config/config.yaml is missing, the installer downloads config.example.yaml there.
  - With --version, the example config is fetched from the matching release tag.
  - The installer creates a symlink at /usr/local/bin/syrogo so the command is available from PATH.
  - The installer keeps an existing installed config by default.
  - Pass --force-config if you want to replace /opt/syrogo/config/config.yaml from --config.
  - --uninstall removes the default config together with /opt/syrogo.
EOF
}

log() {
  if [ "$BOOTSTRAP" -eq 1 ]; then
    printf '[install] %s\n' "$*" >&2
  else
    printf '[install] %s\n' "$*"
  fi
}

fail() {
  printf '[install] %s\n' "$*" >&2
  exit 1
}

curl_supports_retry_all_errors() {
  curl --retry-all-errors --version >/dev/null 2>&1
}

curl_common_args() {
  CURL_ARGS=(
    -fL
    --retry "$CURL_RETRY"
    --connect-timeout "$CURL_CONNECT_TIMEOUT"
    --max-time "$CURL_MAX_TIME"
    --speed-limit "$CURL_LOW_SPEED_LIMIT"
    --speed-time "$CURL_LOW_SPEED_TIME"
  )
  if curl_supports_retry_all_errors; then
    CURL_ARGS+=(--retry-all-errors)
  fi
  if [ -n "$DOWNLOAD_PROXY" ]; then
    CURL_ARGS+=(--proxy "$DOWNLOAD_PROXY")
  fi
}

curl_text() {
  local url
  url="$1"
  command -v curl >/dev/null 2>&1 || fail "curl is required"
  if [ -n "$DOWNLOAD_PROXY" ]; then
    log "using download proxy: $DOWNLOAD_PROXY"
  fi
  curl_common_args
  curl -fsSL "${CURL_ARGS[@]}" "$url"
}

curl_download() {
  local url output
  url="$1"
  output="$2"
  command -v curl >/dev/null 2>&1 || fail "curl is required"
  if [ -n "$DOWNLOAD_PROXY" ]; then
    log "using download proxy: $DOWNLOAD_PROXY"
  fi
  curl_common_args
  curl "${CURL_ARGS[@]}" "$url" -o "$output"
}

curl_download_resume() {
  local url output attempt max_attempts
  url="$1"
  output="$2"
  max_attempts="$CURL_RETRY"
  command -v curl >/dev/null 2>&1 || fail "curl is required"
  if [ -n "$DOWNLOAD_PROXY" ]; then
    log "using download proxy: $DOWNLOAD_PROXY"
  fi
  attempt=1
  while [ "$attempt" -le "$max_attempts" ]; do
    CURL_ARGS=(
      -fL
      --connect-timeout "$CURL_CONNECT_TIMEOUT"
      --max-time "$CURL_MAX_TIME"
      --speed-limit "$CURL_LOW_SPEED_LIMIT"
      --speed-time "$CURL_LOW_SPEED_TIME"
    )
    if [ -n "$DOWNLOAD_PROXY" ]; then
      CURL_ARGS+=(--proxy "$DOWNLOAD_PROXY")
    fi
    if curl "${CURL_ARGS[@]}" -C - "$url" -o "$output"; then
      return 0
    fi
    log "download attempt $attempt failed; retrying with resume"
    attempt=$((attempt + 1))
  done
  return 1
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
      --bootstrap)
        BOOTSTRAP=1
        shift
        ;;
      --uninstall)
        UNINSTALL=1
        shift
        ;;
      --purge-config)
        PURGE_CONFIG=1
        shift
        ;;
      --config)
        [ "$#" -ge 2 ] || fail "missing value for --config"
        CONFIG_SOURCE="$2"
        CONFIG_SOURCE_EXPLICIT=1
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
      --symlink)
        [ "$#" -ge 2 ] || fail "missing value for --symlink"
        SYMLINK_PATH="$2"
        shift 2
        ;;
      --proxy)
        [ "$#" -ge 2 ] || fail "missing value for --proxy"
        DOWNLOAD_PROXY="$2"
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
  DEFAULT_CONFIG_SOURCE="$CONFIG_PATH"
  if [ "$CONFIG_SOURCE_EXPLICIT" -eq 0 ]; then
    CONFIG_SOURCE="$DEFAULT_CONFIG_SOURCE"
  fi
  SYSTEMD_UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

  if [ "$UNINSTALL" -eq 1 ] && { [ -n "$ARCHIVE" ] || [ -n "$VERSION" ] || [ "$FORCE_CONFIG" -eq 1 ] || [ "$SKIP_HEALTHCHECK" -eq 1 ]; }; then
    fail "--uninstall cannot be combined with install or healthcheck flags"
  fi

  if [ "$PURGE_CONFIG" -eq 1 ] && [ "$UNINSTALL" -ne 1 ]; then
    fail "--purge-config requires --uninstall"
  fi

  if [ "$BOOTSTRAP" -eq 1 ]; then
    [ -n "$VERSION" ] || fail "--bootstrap requires an explicit non-empty --version"
    [ -z "$ARCHIVE" ] || fail "--bootstrap cannot be combined with --archive"
    [ "$UNINSTALL" -eq 0 ] || fail "--bootstrap cannot be combined with --uninstall"
    [ "$FORCE_CONFIG" -eq 0 ] || fail "--bootstrap cannot be combined with --force-config"
    [ "$CONFIG_SOURCE_EXPLICIT" -eq 0 ] || fail "--bootstrap cannot be combined with --config"
    if [ -e "$CONFIG_PATH" ] || [ -L "$CONFIG_PATH" ]; then
      fail "--bootstrap requires a new config; refusing to overwrite: $CONFIG_PATH"
    fi

    BOOTSTRAP_ADMIN_TOKEN="${SYROGO_BOOTSTRAP_ADMIN_TOKEN:-}"
    unset SYROGO_BOOTSTRAP_ADMIN_TOKEN
    [ -n "$BOOTSTRAP_ADMIN_TOKEN" ] || fail "--bootstrap requires non-empty SYROGO_BOOTSTRAP_ADMIN_TOKEN"
    case "$BOOTSTRAP_ADMIN_TOKEN" in
      *[!A-Za-z0-9_-]*) fail "SYROGO_BOOTSTRAP_ADMIN_TOKEN must be hex or URL-safe" ;;
    esac
  fi
}

resolve_latest_version() {
  local api_url tag
  command -v curl >/dev/null 2>&1 || fail "curl is required to resolve the latest release"
  api_url="https://api.github.com/repos/${REPO}/releases/latest"
  log "resolving latest release from GitHub API"
  tag="$(curl_text "$api_url" | sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  [ -n "$tag" ] || fail "failed to resolve latest release tag, please pass --version explicitly"
  VERSION="$tag"
  log "resolved latest release: $VERSION"
}

config_init_url() {
  if [ "$BOOTSTRAP" -eq 1 ]; then
    printf 'https://raw.githubusercontent.com/%s/refs/tags/%s/configs/config.console-bootstrap.yaml' "$REPO" "$VERSION"
    return
  fi

  if [ -n "$VERSION" ]; then
    printf 'https://raw.githubusercontent.com/%s/refs/tags/%s/configs/config.example.yaml' "$REPO" "$VERSION"
    return
  fi

  printf 'https://raw.githubusercontent.com/%s/refs/heads/master/configs/config.example.yaml' "$REPO"
}

generate_bootstrap_client_token() {
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

download_bootstrap_config() {
  local url bootstrap_tmp admin_count client_count line rendered
  command -v curl >/dev/null 2>&1 || fail "curl is required to initialize the bootstrap config"
  command -v od >/dev/null 2>&1 || fail "od is required to generate the bootstrap client token"

  install -d -m 0755 "$(dirname "$DEFAULT_CONFIG_SOURCE")"
  bootstrap_tmp="$(mktemp "$(dirname "$DEFAULT_CONFIG_SOURCE")/.config.bootstrap.XXXXXX")"
  chmod 0600 "$bootstrap_tmp"
  url="$(config_init_url)"
  log "downloading Console bootstrap config"
  log "config source url: $url"
  if ! curl_download "$url" "$bootstrap_tmp"; then
    rm -f "$bootstrap_tmp"
    fail "failed to download Console bootstrap config"
  fi

  admin_count="$({ grep -o '__SYROGO_CONSOLE_ADMIN_TOKEN__' "$bootstrap_tmp" || true; } | wc -l | tr -d '[:space:]')"
  client_count="$({ grep -o '__SYROGO_CONSOLE_CLIENT_TOKEN__' "$bootstrap_tmp" || true; } | wc -l | tr -d '[:space:]')"
  if [ "$admin_count" != "1" ] || [ "$client_count" != "1" ]; then
    rm -f "$bootstrap_tmp"
    fail "bootstrap config must contain exactly one admin and one client token placeholder"
  fi

  if ! BOOTSTRAP_CLIENT_TOKEN="$(generate_bootstrap_client_token)" || [ "${#BOOTSTRAP_CLIENT_TOKEN}" -ne 64 ]; then
    rm -f "$bootstrap_tmp"
    fail "failed to generate bootstrap client token"
  fi

  rendered="${bootstrap_tmp}.rendered"
  : > "$rendered"
  chmod 0600 "$rendered"
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line//__SYROGO_CONSOLE_ADMIN_TOKEN__/$BOOTSTRAP_ADMIN_TOKEN}"
    line="${line//__SYROGO_CONSOLE_CLIENT_TOKEN__/$BOOTSTRAP_CLIENT_TOKEN}"
    printf '%s\n' "$line" >> "$rendered"
  done < "$bootstrap_tmp"
  rm -f "$bootstrap_tmp"

  if grep -q '__SYROGO_CONSOLE_.*_TOKEN__' "$rendered"; then
    rm -f "$rendered"
    fail "bootstrap config contains unreplaced token placeholders"
  fi
  if ! ln "$rendered" "$DEFAULT_CONFIG_SOURCE"; then
    rm -f "$rendered"
    fail "bootstrap config appeared during installation; refusing to overwrite: $DEFAULT_CONFIG_SOURCE"
  fi
  rm -f "$rendered"
  BOOTSTRAP_ADMIN_TOKEN=""
  CONFIG_INITIALIZED=1
}

download_default_config() {
  local url
  if [ "$BOOTSTRAP" -eq 1 ]; then
    download_bootstrap_config
    return
  fi

  command -v curl >/dev/null 2>&1 || fail "curl is required to initialize the default config"
  install -d -m 0755 "$(dirname "$DEFAULT_CONFIG_SOURCE")"
  url="$(config_init_url)"
  log "downloading example config to $DEFAULT_CONFIG_SOURCE"
  log "config source url: $url"
  curl_download "$url" "$DEFAULT_CONFIG_SOURCE"
  CONFIG_INITIALIZED=1
}

validate_config_input() {
  if [ -f "$CONFIG_PATH" ] && [ "$FORCE_CONFIG" -eq 0 ]; then
    log "keeping existing config: $CONFIG_PATH"
    return
  fi

  if [ -f "$CONFIG_SOURCE" ]; then
    return
  fi

  if [ "$CONFIG_SOURCE" = "$DEFAULT_CONFIG_SOURCE" ]; then
    download_default_config
    return
  fi

  fail "config file not found: $CONFIG_SOURCE"
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

verify_release_archive() {
  local archive_name checksum_file checksum_name checksum_url expected actual file
  archive_name="$(basename "$ARCHIVE")"
  checksum_name="syrogo_${VERSION}_checksums.txt"
  checksum_file="$TMP_DIR/$checksum_name"
  checksum_url="https://github.com/${REPO}/releases/download/${VERSION}/${checksum_name}"

  command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required to verify release downloads"
  log "downloading ${checksum_url}"
  curl_download "$checksum_url" "$checksum_file"

  expected=""
  while read -r hash file; do
    file="${file#\*}"
    if [ "$file" = "$archive_name" ]; then
      expected="$hash"
      break
    fi
  done < "$checksum_file"
  [ -n "$expected" ] || fail "checksum for $archive_name not found in $checksum_name"
  case "$expected" in
    *[!0-9A-Fa-f]*|'') fail "invalid checksum for $archive_name in $checksum_name" ;;
  esac
  [ "${#expected}" -eq 64 ] || fail "invalid checksum for $archive_name in $checksum_name"

  expected="${expected,,}"
  actual="$(sha256sum "$ARCHIVE")"
  actual="${actual%% *}"
  [ "$actual" = "$expected" ] || fail "checksum verification failed for $archive_name"
  log "verified release checksum: $archive_name"
}

download_archive() {
  local arch url
  arch="$(detect_arch)"
  TMP_DIR="$(mktemp -d)"
  ARCHIVE="$TMP_DIR/syrogo_${VERSION}_linux_${arch}.tar.gz"
  url="https://github.com/${REPO}/releases/download/${VERSION}/syrogo_${VERSION}_linux_${arch}.tar.gz"
  log "downloading ${url}"
  curl_download_resume "$url" "$ARCHIVE"
  verify_release_archive
}

ensure_service_user() {
  if id "$SERVICE_USER" >/dev/null 2>&1; then
    return
  fi
  useradd --system --home-dir "$INSTALL_ROOT" --shell /usr/sbin/nologin "$SERVICE_USER"
}

ensure_install_directories() {
  install -d -m 0755 "$INSTALL_ROOT/bin" "$INSTALL_ROOT/config" "$INSTALL_ROOT/logs" "$INSTALL_ROOT/tmp"
  if [ ! -d "$INSTALL_ROOT/data" ]; then
    install -d -m 0700 "$INSTALL_ROOT/data"
  fi
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

  ensure_install_directories
  install -m 0755 "$binary_source" "$BIN_PATH"
}

install_symlink() {
  local symlink_dir current_target
  symlink_dir="$(dirname "$SYMLINK_PATH")"
  install -d -m 0755 "$symlink_dir"
  if [ -e "$SYMLINK_PATH" ] || [ -L "$SYMLINK_PATH" ]; then
    if [ -L "$SYMLINK_PATH" ]; then
      current_target="$(readlink "$SYMLINK_PATH")"
      if [ "$current_target" = "$BIN_PATH" ]; then
        return
      fi
    fi
    fail "command path already exists and is not managed by this installer: $SYMLINK_PATH"
  fi
  ln -s "$BIN_PATH" "$SYMLINK_PATH"
  log "installed command symlink: $SYMLINK_PATH -> $BIN_PATH"
}
install_or_keep_config() {
  if [ -f "$CONFIG_PATH" ] && [ "$FORCE_CONFIG" -eq 0 ]; then
    log "config unchanged: $CONFIG_PATH"
    return
  fi

  if [ "$CONFIG_SOURCE" = "$CONFIG_PATH" ]; then
    chmod 0600 "$CONFIG_PATH"
    CONFIG_UPDATED=1
    log "using config in place: $CONFIG_PATH"
    return
  fi

  install -m 0600 "$CONFIG_SOURCE" "$CONFIG_PATH"
  CONFIG_UPDATED=1
  log "installed config from $CONFIG_SOURCE"
}

write_version_receipt() {
  local receipt_version
  receipt_version="$VERSION"
  if [ -z "$receipt_version" ]; then
    receipt_version="local-archive"
    log "local archive supplied; release checksum was not verified"
  fi
  printf '%s\n' "$receipt_version" > "$INSTALL_ROOT/VERSION"
  chmod 0644 "$INSTALL_ROOT/VERSION"
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

remove_symlink() {
  local current_target
  if [ ! -L "$SYMLINK_PATH" ]; then
    if [ -e "$SYMLINK_PATH" ]; then
      log "command path exists but is not a symlink, leaving unchanged: $SYMLINK_PATH"
    else
      log "command symlink not found: $SYMLINK_PATH"
    fi
    return
  fi

  current_target="$(readlink "$SYMLINK_PATH")"
  if [ "$current_target" != "$BIN_PATH" ]; then
    log "command symlink points elsewhere, leaving unchanged: $SYMLINK_PATH -> $current_target"
    return
  fi

  rm -f "$SYMLINK_PATH"
  log "removed command symlink: $SYMLINK_PATH"
}

uninstall_service() {
  if [ "$INSTALL_ROOT" != "/opt/syrogo" ]; then
    fail "refusing to uninstall unexpected install root: $INSTALL_ROOT"
  fi

  if [ -f "$SYSTEMD_UNIT_PATH" ]; then
    systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
    systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true
    rm -f "$SYSTEMD_UNIT_PATH"
    log "removed service unit: $SYSTEMD_UNIT_PATH"
  else
    log "service unit not found: $SYSTEMD_UNIT_PATH"
  fi

  systemctl daemon-reload

  remove_symlink

  if [ -d "$INSTALL_ROOT" ]; then
    rm -rf "$INSTALL_ROOT"
    log "removed install root: $INSTALL_ROOT"
  else
    log "install root not found: $INSTALL_ROOT"
  fi

  if [ "$PURGE_CONFIG" -eq 1 ]; then
    log "--purge-config has no extra effect because the default config lives under $INSTALL_ROOT"
  fi

  log "uninstalled Syrogo from $INSTALL_ROOT"
}

start_service() {
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME" >/dev/null
  systemctl restart "$SERVICE_NAME"
}

healthcheck() {
  [ "$SKIP_HEALTHCHECK" -eq 1 ] && return
  command -v curl >/dev/null 2>&1 || fail "curl is required for the final health check"
  log "checking service health: $HEALTH_URL"
  for _ in 1 2 3 4 5; do
    if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
      log "health check passed: $HEALTH_URL"
      return
    fi
    sleep 1
  done
  fail "service started but health check is not ready yet: $HEALTH_URL"
}

main() {
  require_root
  require_linux_systemd
  parse_args "$@"
  if [ "$UNINSTALL" -eq 1 ]; then
    uninstall_service
    return
  fi
  if [ -z "$ARCHIVE" ] && [ -z "$VERSION" ]; then
    resolve_latest_version
  fi
  validate_config_input
  if [ -n "$VERSION" ]; then
    download_archive
  fi
  ensure_service_user
  extract_binary
  install_symlink
  install_or_keep_config
  install_unit
  chown -R "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_ROOT"
  start_service
  healthcheck
  write_version_receipt
  chown "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_ROOT/VERSION"
  if [ "$BOOTSTRAP" -eq 1 ]; then
    [ -n "$BOOTSTRAP_CLIENT_TOKEN" ] || fail "bootstrap client token was not generated"
    printf '%s\n' "$BOOTSTRAP_CLIENT_TOKEN"
    return
  fi
  log "installed Syrogo to $INSTALL_ROOT"
  log "command path: $SYMLINK_PATH"
  log "config path: $CONFIG_PATH"
  if [ "$CONFIG_INITIALIZED" -eq 1 ]; then
    log "example config initialized at $DEFAULT_CONFIG_SOURCE"
  fi
  if [ "$CONFIG_UPDATED" -eq 1 ]; then
    log "please review the config and restart the service after any changes: systemctl restart $SERVICE_NAME"
  fi
}

if [ "${SYROGO_INSTALL_TEST_MODE:-0}" -ne 1 ]; then
  main "$@"
fi
