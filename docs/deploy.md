# Deploy Syrogo and SyrogoConsole

[中文](./deploy.zh-CN.md) | English

Syrogo can be managed by editing YAML directly. For day-to-day configuration, Provider, Client, Route, observability, and history/rollback operations, the official SyrogoConsole is recommended. The official Linux entrypoint is the SyrogoConsole suite installer; the embedded Core web UI is not part of the installation or recommended path.

## 1. Default topology

```text
Browser -> SyrogoConsole 127.0.0.1:23233
                     /admin/* -> Syrogo Core 127.0.0.1:23234
Model clients -----------------> Syrogo Core protocol inbounds
```

The Console Server hosts the SPA and proxies same-origin `/admin/*` requests to Core. The browser sends the Admin token; the Console Server neither stores nor injects it. Both listeners default to loopback and should not be exposed directly to the public Internet.

Requirements: Linux, `systemd`, root, `curl`, `bash`, and `sha256sum`.

## 2. Recommended: install the suite on a new host

Install the latest stable Console release:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh | sudo bash
```

Pin Core and Console to the same version:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo bash -s -- --version v0.16.3
```

On a completely empty host, the installer:

1. Installs the matching Syrogo Core under `/opt/syrogo`.
2. Creates a safe bootstrap config bound only to `127.0.0.1:23234`.
3. Installs SyrogoConsole under `/opt/syrogo-console`.
4. Creates and starts `syrogo.service` and `syrogo-console.service`.
5. Prints the Core Admin token required for the first Console login.

The bootstrap config uses the `mock` outbound and contains no third-party Provider key. Add real Providers, Clients, and Routes after signing in to Console.

## 3. Install Console with an existing Core

Run the same Console installer. If a healthy Core exists in the default location, it is reused without upgrades, restarts, or config changes:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh | sudo bash
```

To require an existing Core explicitly:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo bash -s -- --console-only
```

The existing Core must have:

- `/opt/syrogo/bin/syrogo`, `/opt/syrogo/config/config.yaml`, and `syrogo.service` present.
- A healthy `http://127.0.0.1:23234/healthz` endpoint.
- `admin` enabled with an Admin token you control.

The installer fails closed for an incomplete or unhealthy Core. Repair Core first and retry; `--with-core` does not overwrite such an installation either.

## 4. Install and manage Core only

For fully manual YAML management, the standalone Core installer remains available:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash
```

Pin a version:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh \
  | sudo bash -s -- --version v0.16.3
```

The Core installer preserves `/opt/syrogo/config/config.yaml` unless `--force-config` is explicit. Release archives are verified against the matching Release checksum, the installed version is recorded in `/opt/syrogo/VERSION`, and new configs use mode `0600`.

## 5. Access Console

Open locally:

```text
http://127.0.0.1:23233
```

For remote administration, use an SSH tunnel:

```bash
ssh -L 23233:127.0.0.1:23233 user@server
```

Then open `http://127.0.0.1:23233` locally. For a long-lived cross-host management endpoint, put a trusted TLS reverse proxy and access control in front of Console. Do not expose the plaintext management plane directly or store the Admin token in proxy configuration.

## 6. Configure and verify

Core config lives at:

```text
/opt/syrogo/config/config.yaml
```

Manage it through Console or edit YAML manually. After manual changes, use Console Apply or the Admin API; listener addresses, listener bindings, and some logging changes still require a Core restart.

Check both services:

```bash
curl http://127.0.0.1:23234/healthz
curl http://127.0.0.1:23233/healthz
sudo systemctl status syrogo syrogo-console
sudo journalctl -u syrogo -u syrogo-console -f
```

Then verify an actual protocol endpoint such as `POST /v1/chat/completions`, `POST /v1/responses`, or `POST /v1/messages`.

## 7. Upgrade

Use the same SemVer for a combined Core and Console deployment. Confirm the target Core Release exists first. The Console installer reuses a healthy Core and does not upgrade it:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh \
  | sudo bash -s -- --version v0.16.3
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo bash -s -- --version v0.16.3 --console-only
```

Back up `/opt/syrogo/config/config.yaml` first, then verify both `/healthz` endpoints and one real model request.

## 8. Uninstall

Remove Console without changing Core:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo bash -s -- --uninstall
```

Standalone Core uninstall removes `/opt/syrogo`, including its config:

```bash
sudo bash ./scripts/install.sh --uninstall
```

## 9. Current boundary

The official installer currently covers Linux + `systemd` amd64/arm64 binary deployments. It does not yet provide Windows/macOS one-command installation, Docker, Kubernetes, Helm, system packages, automatic TLS, or public access control.
