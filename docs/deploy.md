# Deploy Syrogo

[中文](./deploy.zh-CN.md) | English

This guide targets the `v0.1.x` baseline release of Syrogo and focuses on the smallest production-like deployment path.

It covers:
- downloading a release archive
- preparing a runnable config
- starting the service
- running Syrogo with `systemd`
- basic upgrade and troubleshooting steps

---

## 1. Choose a release asset

Download the archive that matches your host:

- `syrogo_v0.1.x_linux_amd64.tar.gz`
- `syrogo_v0.1.x_linux_arm64.tar.gz`
- `syrogo_v0.1.x_darwin_amd64.tar.gz`
- `syrogo_v0.1.x_darwin_arm64.tar.gz`

Then extract it:

```bash
tar -xzf syrogo_v0.1.x_linux_amd64.tar.gz
cd syrogo_linux_amd64
```

The archive contains:
- `syrogo`
- `README.md`
- `LICENSE`

---

## 2. Prepare directories

A minimal Linux layout can be:

```text
/opt/syrogo/
  bin/syrogo
  config/config.yaml
  logs/
  tmp/
```

Example:

```bash
sudo mkdir -p /opt/syrogo/bin /opt/syrogo/config /opt/syrogo/logs /opt/syrogo/tmp
sudo cp syrogo /opt/syrogo/bin/
sudo chmod +x /opt/syrogo/bin/syrogo
```

---

## 3. Prepare config

Start from the repository example config:

```bash
cp configs/config.example.yaml config.yaml
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

## 4. Start the service manually

Run Syrogo with an explicit config path:

```bash
/opt/syrogo/bin/syrogo -config /opt/syrogo/config/config.yaml
```

For local-style troubleshooting on a server, you can temporarily enable dev logging:

```bash
/opt/syrogo/bin/syrogo -config /opt/syrogo/config/config.yaml -dev-log
```

---

## 5. Verify health and routing

Check health first:

```bash
curl http://127.0.0.1:8080/healthz
```

Then verify one of the protocol entrypoints you actually expose.

Recommended first checks:
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

If you want the smallest smoke path, point one route to a `mock` outbound first.

---

## 6. Run with systemd

Example unit file:

```ini
[Unit]
Description=Syrogo AI Gateway
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/syrogo
ExecStart=/opt/syrogo/bin/syrogo -config /opt/syrogo/config/config.yaml
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

Save it as:

```text
/etc/systemd/system/syrogo.service
```

Then enable and start it:

```bash
sudo systemctl daemon-reload
sudo systemctl enable syrogo
sudo systemctl start syrogo
```

Useful commands:

```bash
sudo systemctl status syrogo
sudo journalctl -u syrogo -f
sudo systemctl restart syrogo
```

---

## 7. Upgrade procedure

A minimal upgrade flow is:

1. download the new release archive
2. extract the new `syrogo` binary
3. replace `/opt/syrogo/bin/syrogo`
4. keep the existing config file
5. restart the service
6. verify `/healthz` and one real protocol request

Example:

```bash
sudo systemctl stop syrogo
sudo cp syrogo /opt/syrogo/bin/syrogo
sudo chmod +x /opt/syrogo/bin/syrogo
sudo systemctl start syrogo
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
- Docker images
- Kubernetes manifests
- Helm charts
- Homebrew / apt packages
- signing, notarization, or SBOM workflows

The current goal is a small, understandable binary deployment path that is easy to verify and maintain.
