# Deploy Syrogo

[中文](./deploy.zh-CN.md) | English

This guide targets the `v0.1.x` baseline release of Syrogo and focuses on the smallest production-like deployment path.

It covers:
- preparing a runnable config on the target host
- local and remote one-command installation on Linux
- running Syrogo with `systemd`
- upgrades with the same installer path
- basic troubleshooting steps

---

## 1. Prepare config on the target host

The installer expects a local config file on the target machine.

Default path:

```text
/etc/syrogo/config.yaml
```

On first install, if that path does not exist yet, the installer downloads `configs/config.example.yaml` automatically:
- with `--version`, from the matching release tag
- with `--archive`, from `master`

A practical way to prepare it manually is:

```bash
sudo mkdir -p /etc/syrogo
sudo cp configs/config.example.yaml /etc/syrogo/config.yaml
```

Then replace placeholder values with real values for your environment.

At minimum, verify these areas:
- `server.listen` or `listeners[]`
- inbound client tokens
- outbound `endpoint`
- outbound `auth_token`
- routing rules from `from_tags` to `to_tags`

Important:
- the current implementation does not auto-load `.env`
- `${VAR}` placeholders are not expanded automatically
- if a placeholder stays in the file, it is used as a literal string

---

## 2. Install on Linux

Syrogo provides one installer entrypoint for Linux hosts with `systemd`.

### Local execution

From a checked-out repository, run one of these:

```bash
sudo bash ./scripts/install.sh
```

```bash
sudo bash ./scripts/install.sh --version v0.1.0
```

```bash
sudo bash ./scripts/install.sh --archive ./syrogo_v0.1.0_linux_amd64.tar.gz
```

### Remote `curl | bash`

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s --
```

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s -- --version v0.1.0
```

### Optional config override

If your config is stored somewhere else on the host, override the source path explicitly:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s -- --version v0.1.0 --config /path/to/config.yaml
```

The installer will:
- install Syrogo into `/opt/syrogo`
- install the binary into `/opt/syrogo/bin/syrogo`
- install `syrogo.service` into `/etc/systemd/system/syrogo.service`
- enable and restart the `syrogo` service
- run a final `/healthz` check against `http://127.0.0.1:23234/healthz`

Current boundary:
- Linux only
- `systemd` required
- root privileges required
- does not generate a full config for you
- does not provision TLS, nginx, Docker, or Kubernetes

---

## 3. Config overwrite behavior

By default, the installer keeps the already installed config at:

```text
/opt/syrogo/config/config.yaml
```

That means:
- first install auto-initializes `/etc/syrogo/config.yaml` if the default source path is missing
- with `--version`, the initialized example config comes from the matching release tag
- with `--archive`, the initialized example config comes from `master`
- the installer copies that local source into `/opt/syrogo/config/config.yaml`
- upgrades reuse the installed config by default
- rerunning the installer does not overwrite the installed config unless you ask it to

If you really want to replace the installed config, pass `--force-config`:

```bash
sudo bash ./scripts/install.sh --version v0.1.1 --config /etc/syrogo/config.yaml --force-config
```

---

## 4. Upgrade procedure

Upgrades use the same installer path as the first installation.

Example:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s -- --version v0.1.1
```

or:

```bash
sudo bash ./scripts/install.sh --version v0.1.1
```

A minimal upgrade flow is:
1. update the local config file only if needed
2. rerun the installer with the new version
3. verify `/healthz` and one real protocol request

---

## 5. Start the service manually

If you do not want the installer path, you can still run Syrogo directly with an explicit config path:

```bash
/opt/syrogo/bin/syrogo -config /opt/syrogo/config/config.yaml
```

For local-style troubleshooting on a server, you can temporarily enable dev logging:

```bash
/opt/syrogo/bin/syrogo -config /opt/syrogo/config/config.yaml -dev-log
```

---

## 6. Verify health and routing

Check health first:

```bash
curl http://127.0.0.1:23234/healthz
```

Then verify one of the protocol entrypoints you actually expose.

Recommended first checks:
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

If you want the smallest smoke path, point one route to a `mock` outbound first.

---

## 7. Run with systemd

The installer renders the unit file into:

```text
/etc/systemd/system/syrogo.service
```

Useful commands:

```bash
sudo systemctl status syrogo
sudo journalctl -u syrogo -f
sudo systemctl restart syrogo
```

---

## 8. Reverse proxy and network notes

Syrogo can be exposed directly or placed behind a reverse proxy.

For `v0.1.x`, keep the deployment simple:
- bind to an internal port first
- expose through nginx or another gateway if needed
- restrict access to trusted clients and tokens
- avoid exposing debug-heavy modes in normal production traffic

If you use a reverse proxy, make sure the target path and listening port match your configured inbound paths.

---

## 9. Troubleshooting

### The installer fails before startup

Check:
- you are on Linux
- `systemd` is available
- you ran the installer as root
- the local config path exists on the target host
- the release archive path or tag is correct

### The service starts but requests fail

Check:
- inbound token values
- outbound `endpoint`
- outbound `auth_token`
- route tag matching
- whether the target upstream is reachable

### Health is OK but model calls fail

This usually means the server is running but one of these is wrong:
- route selection
- outbound auth
- upstream compatibility boundary
- request shape expected by the upstream

### I need more diagnostics

Temporarily enable:

- `-dev-log`

Turn off extra debug output after troubleshooting.

---

## 10. Current deployment boundary

For `v0.1.x`, this guide does not yet cover:
- Windows deployment
- macOS one-command install
- Docker images
- Kubernetes manifests
- Helm charts
- Homebrew / apt packages
- signing, notarization, or SBOM workflows

The current goal is a small, understandable binary deployment path that is easy to verify and maintain.
