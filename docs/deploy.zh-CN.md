# 部署 Syrogo

中文 | [English](./deploy.md)

本文面向 Syrogo `v0.1.x` 基线版本，重点提供一条**最小可用**的安装与部署路径。

覆盖内容包括：
- 在目标机器上准备可运行配置
- Linux 本地与远程一键安装
- 使用 `systemd` 托管
- 用同一入口完成升级
- 基础排障

---

## 1. 在目标机器上准备配置

安装器要求目标机器本地已经有配置文件。

默认路径：

```text
/etc/syrogo/config.yaml
```

首次安装时，如果这个路径还不存在，安装器会自动拉取 `configs/config.example.yaml`：
- 使用 `--version` 时，拉对应 release tag 下的样例
- 使用 `--archive` 时，拉 `master` 上的样例

如果你想手工提前准备，也可以这样做：

```bash
sudo mkdir -p /etc/syrogo
sudo cp configs/config.example.yaml /etc/syrogo/config.yaml
```

然后把占位值替换成你环境中的真实值。

至少要核对这些部分：
- `server.listen` 或 `listeners[]`
- inbound client token
- outbound `endpoint`
- outbound `auth_token`
- `from_tags -> to_tags` 路由规则

注意：
- 当前实现不会自动读取 `.env`
- `${VAR}` 占位符不会自动展开
- 如果配置里保留占位符字符串，它会被当作普通字符串直接使用

---

## 2. Linux 一键安装

Syrogo 提供了一个统一安装入口，面向 Linux + `systemd` 主机。

### 本地执行

在仓库目录下可执行以下任一方式：

```bash
sudo bash ./scripts/install.sh
```

```bash
sudo bash ./scripts/install.sh --version v0.1.0
```

```bash
sudo bash ./scripts/install.sh --archive ./syrogo_v0.1.0_linux_amd64.tar.gz
```

### 远程 `curl | bash`

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s --
```

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s -- --version v0.1.0
```

### 覆盖默认配置路径

如果你的配置文件不在默认位置，也可以显式指定：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s -- --version v0.1.0 --config /path/to/config.yaml
```

安装器会自动：
- 安装到 `/opt/syrogo`
- 把二进制安装到 `/opt/syrogo/bin/syrogo`
- 安装 `syrogo.service` 到 `/etc/systemd/system/syrogo.service`
- 启用并重启 `syrogo` 服务
- 最后对 `http://127.0.0.1:23234/healthz` 做一次健康检查

当前边界：
- 仅支持 Linux
- 依赖 `systemd`
- 需要 root 权限
- 不会替你自动生成完整配置
- 不处理 TLS、nginx、Docker、Kubernetes

---

## 3. 配置覆盖行为

默认情况下，安装器会保留已经安装好的配置：

```text
/opt/syrogo/config/config.yaml
```

这意味着：
- 首次安装时，如果默认配置源路径不存在，安装器会先自动初始化 `/etc/syrogo/config.yaml`
- 使用 `--version` 时，初始化样例来自对应 release tag
- 使用 `--archive` 时，初始化样例来自 `master`
- 安装器会再把这个本地配置复制到 `/opt/syrogo/config/config.yaml`
- 后续升级默认复用已安装配置
- 重复执行安装器时，不会覆盖已安装配置，除非你显式要求

如果你确实要替换已安装配置，可传 `--force-config`：

```bash
sudo bash ./scripts/install.sh --version v0.1.1 --config /etc/syrogo/config.yaml --force-config
```

---

## 4. 升级流程

升级与首次安装使用同一条安装入口。

示例：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s -- --version v0.1.1
```

或者：

```bash
sudo bash ./scripts/install.sh --version v0.1.1
```

一个最小升级流程如下：
1. 只在需要时更新目标机器上的本地配置文件
2. 用新版本号重新执行安装器
3. 验证 `/healthz` 和一条真实协议请求

---

## 5. 手工启动服务

如果你不走安装器，也可以继续用显式配置路径手工启动：

```bash
/opt/syrogo/bin/syrogo -config /opt/syrogo/config/config.yaml
```

如果是在服务器上做临时排查，也可以短时间开启开发日志：

```bash
/opt/syrogo/bin/syrogo -config /opt/syrogo/config/config.yaml -dev-log
```

---

## 6. 验证健康状态与路由

先检查健康状态：

```bash
curl http://127.0.0.1:23234/healthz
```

然后再验证你实际暴露的协议入口之一。

建议优先验证：
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

如果只想先走最小 smoke test，可以先把某条 route 指向 `mock` outbound。

---

## 7. 使用 systemd 托管

安装器会把 unit 渲染到：

```text
/etc/systemd/system/syrogo.service
```

常用命令：

```bash
sudo systemctl status syrogo
sudo journalctl -u syrogo -f
sudo systemctl restart syrogo
```

---

## 8. 卸载

如果只删除服务和 `/opt/syrogo` 下的安装内容，但保留配置：

```bash
sudo bash ./scripts/install.sh --uninstall
```

如果连默认配置文件一起删除：

```bash
sudo bash ./scripts/install.sh --uninstall --purge-config
```

---

## 9. 反向代理与网络说明

Syrogo 可以直接暴露，也可以放在反向代理后面。

对 `v0.1.x`，建议保持简单：
- 先监听内网端口
- 需要时再通过 nginx 或其他网关对外暴露
- 只对受信任客户端发放 token
- 不要在常规生产流量上长期开启重调试模式

如果前面有反向代理，要确保转发路径和监听端口与你配置的 inbound 路径一致。

---

## 9. 常见排障

### 安装脚本在启动前失败

优先检查：
- 当前主机是否是 Linux
- 是否存在 `systemd`
- 是否以 root 身份执行
- 目标机器上的本地配置路径是否存在
- release 压缩包路径或 tag 是否正确

### 服务能启动，但请求失败

优先检查：
- inbound token 是否正确
- outbound `endpoint` 是否正确
- outbound `auth_token` 是否正确
- route tag 是否命中
- 目标上游是否可达

### `/healthz` 正常，但模型调用失败

这通常说明服务本身已经启动，但下面某一层有问题：
- route 选择
- outbound 鉴权
- 上游兼容边界
- 上游实际期待的请求形状

### 我需要更多诊断信息

可临时开启：

- `-dev-log`

排查结束后，关闭额外调试输出。

---

## 10. 当前部署边界

`v0.1.x` 当前还不覆盖：
- Windows 部署
- macOS 一键安装
- Docker 镜像
- Kubernetes manifests
- Helm charts
- Homebrew / apt 包
- 签名、公证与 SBOM 流程

当前目标是先提供一条小而清晰、容易验证、容易维护的二进制部署路径。
