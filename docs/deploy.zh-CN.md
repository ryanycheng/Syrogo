# 部署 Syrogo 与 SyrogoConsole

中文 | [English](./deploy.md)

Syrogo 支持直接编辑 YAML 配置；日常配置、Provider、Client、Route、观测和历史回滚更推荐使用官方 SyrogoConsole。官方 Linux 部署入口是 SyrogoConsole 一体化安装器，Core 自带 Web 不属于安装或推荐路径。

## 1. 默认拓扑

```text
Browser -> SyrogoConsole 127.0.0.1:23233
                     /admin/* -> Syrogo Core 127.0.0.1:23234
Model clients -----------------> Syrogo Core protocol inbounds
```

Console Server 托管 SPA，并把同源 `/admin/*` 代理到 Core。Admin token 由浏览器发送；Console Server 不保存或注入 token。默认监听均为 loopback，不应直接暴露到公网。

要求：Linux、`systemd`、root、`curl`、`bash`、`sha256sum`。

## 2. 推荐：新主机一体化安装

使用 Console 最新稳定版：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh | sudo bash
```

固定 Core 与 Console 的相同版本：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo bash -s -- --version v0.16.3
```

在完全空的新主机上，安装器会：

1. 安装同版本 Syrogo Core 到 `/opt/syrogo`。
2. 创建仅监听 `127.0.0.1:23234` 的安全 bootstrap 配置。
3. 安装 SyrogoConsole 到 `/opt/syrogo-console`。
4. 创建并启动 `syrogo.service` 与 `syrogo-console.service`。
5. 输出用于首次登录 Console 的 Core Admin token。

bootstrap 配置使用 `mock` outbound，不包含第三方 Provider key。登录 Console 后，再配置真实 Provider、Client 和 Route。

## 3. 已经安装过 Syrogo Core

直接运行同一条 Console installer。若默认位置存在健康 Core，安装器会复用它，不升级、不重启、不改写配置：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh | sudo bash
```

也可明确要求只安装 Console：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo bash -s -- --console-only
```

已有 Core 必须满足：

- `/opt/syrogo/bin/syrogo`、`/opt/syrogo/config/config.yaml` 和 `syrogo.service` 完整存在。
- `http://127.0.0.1:23234/healthz` 健康。
- 配置已启用 `admin` 并设置你掌握的 Admin token。

安装器遇到残缺或不健康 Core 会停止，不猜测、不覆盖。请先修复 Core，再重试；`--with-core` 也不会覆盖这类安装。

## 4. 只安装和管理 Core

需要完全手工管理 YAML 时，可继续使用 Core installer：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash
```

固定版本：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh \
  | sudo bash -s -- --version v0.16.3
```

Core installer 默认保留 `/opt/syrogo/config/config.yaml`，只在显式传入 `--force-config` 时覆盖。release archive 会按同一 Release 的 checksum 校验，安装版本记录在 `/opt/syrogo/VERSION`，新配置权限为 `0600`。

## 5. 访问 Console

本机浏览器访问：

```text
http://127.0.0.1:23233
```

远程管理推荐 SSH tunnel：

```bash
ssh -L 23233:127.0.0.1:23233 user@server
```

然后在本机访问 `http://127.0.0.1:23233`。Console 监听配置持久化在 `/etc/syrogo-console.env`，默认值为 `127.0.0.1:23233`，普通升级不会覆盖；首次升级到支持该文件的版本时，安装器会从旧 systemd unit 迁移已有地址。首次安装或主动修改监听地址可执行：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo env SYROGO_CONSOLE_LISTEN=0.0.0.0:23233 bash
```

监听值必须包含固定端口 `23233`。若必须跨主机长期提供管理入口，应在 Console 外层配置受信任的 TLS 反向代理、访问控制和防火墙；不要直接公开明文管理面，也不要把 Admin token 写入代理配置。

## 6. 配置与验证

Core 配置位于：

```text
/opt/syrogo/config/config.yaml
```

可在 Console 中治理，也可手工编辑 YAML。手工修改后，使用 Console Apply 或 Admin API 热应用；listener 地址、listener 绑定及部分日志配置变化仍需要重启 Core。

Session snapshot 默认启用。示例配置为：

```yaml
sessions:
  snapshot:
    enabled: true
    dir: "./data/sessions"
    flush_interval: "5s"
```

systemd 的 WorkingDirectory 是 `/opt/syrogo`，实际 snapshot 位于 `/opt/syrogo/data/sessions`；目录权限为 `0700`，snapshot 文件为 `0600`。文件包含 host、CWD、command、tmux 等敏感元数据，应限制访问并备份该目录。服务周期写 snapshot，正常关闭时再次 flush；崩溃最多丢失一个 flush interval。重启时原 active session 先变为 `unknown` 并标记 `recovery_pending`，等待 heartbeat 确认，45 秒无确认则转为 `stopped`。该能力仅用于单机本地恢复，不会接管旧 PID；不需要时可显式配置 `sessions.snapshot.enabled: false`。

上游 OAuth 是实验性兼容能力，不替代稳定的 API key。通过 Admin UI 连接 Claude 或 Codex 后，配置只保留 `auth.type` 和 `credential_ref`；access token、refresh token 和 PKCE verifier 不进入 YAML、配置历史、日志或浏览器存储。凭证默认写入 `/opt/syrogo/data/oauth`，目录权限为 `0700`、文件为 `0600`。Claude 使用 PKCE callback URL 交接，Codex 使用 device flow。两条链路依赖消费端 OAuth 与私有兼容接口，并非官方第三方 OAuth 集成，可能受上游协议和服务条款影响；不要提供浏览器 cookie 或 `sessionKey`，也不要将 OAuth outbound 的 endpoint 改为自定义地址。Codex OAuth 不支持流式请求，Admin UI 的 Provider Test 也不会对 OAuth outbound 发起测试请求。

检查服务：

```bash
curl http://127.0.0.1:23234/healthz
curl http://127.0.0.1:23233/healthz
sudo systemctl status syrogo syrogo-console
sudo journalctl -u syrogo -u syrogo-console -f
```

随后验证实际协议入口，例如 `POST /v1/chat/completions`、`POST /v1/responses` 或 `POST /v1/messages`。

## 7. 升级

Core 与 Console 联合部署时使用相同 SemVer。先确认目标 Core Release 已发布，再执行对应版本的两个安装器。Console installer 会复用健康 Core，不负责升级既有 Core：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh \
  | sudo bash -s -- --version v0.16.3
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo bash -s -- --version v0.16.3 --console-only
```

升级前备份 `/opt/syrogo/config/config.yaml`、`/opt/syrogo/data/sessions` 和 `/opt/syrogo/data/oauth`，升级后检查两个 `/healthz` 和一条真实模型请求。Core installer 会保留已有 `/opt/syrogo/data` 及其 mode；Console 的 `/etc/syrogo-console.env` 在普通升级中保持不变。

## 8. 卸载

只卸载 Console，不影响 Core；该操作同时删除 `/etc/syrogo-console.env`：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo bash -s -- --uninstall
```

单独卸载 Core 会删除 `/opt/syrogo` 及其中配置、`/opt/syrogo/data/sessions` 和 `/opt/syrogo/data/oauth`；如需保留 Session snapshot 或 OAuth 凭证，请先备份：

```bash
sudo bash ./scripts/install.sh --uninstall
```

## 9. 当前边界

当前官方安装路径仅覆盖 Linux + `systemd` 的 amd64/arm64 二进制部署，暂不提供 Windows/macOS 一键安装、Docker、Kubernetes、Helm、系统包、自动 TLS 或公网访问控制。
