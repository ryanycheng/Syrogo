# 部署 Syrogo

中文 | [English](./deploy.md)

本文面向 Syrogo `v0.1.x` 基线版本，重点提供一条**最小可用**的安装与部署路径。

覆盖内容包括：
- 下载 release 制品
- 准备可运行配置
- 启动服务
- 使用 `systemd` 托管
- 基础升级与排障

---

## 1. 选择发布制品

根据你的主机平台下载对应压缩包：

- `syrogo_v0.1.x_linux_amd64.tar.gz`
- `syrogo_v0.1.x_linux_arm64.tar.gz`
- `syrogo_v0.1.x_darwin_amd64.tar.gz`
- `syrogo_v0.1.x_darwin_arm64.tar.gz`

下载后解压：

```bash
tar -xzf syrogo_v0.1.x_linux_amd64.tar.gz
cd syrogo_linux_amd64
```

压缩包内包含：
- `syrogo`
- `README.md`
- `LICENSE`

---

## 2. 准备目录

一个最小 Linux 部署目录可以是：

```text
/opt/syrogo/
  bin/syrogo
  config/config.yaml
  logs/
  tmp/
```

示例：

```bash
sudo mkdir -p /opt/syrogo/bin /opt/syrogo/config /opt/syrogo/logs /opt/syrogo/tmp
sudo cp syrogo /opt/syrogo/bin/
sudo chmod +x /opt/syrogo/bin/syrogo
```

---

## 3. 准备配置

建议从仓库示例配置开始：

```bash
cp configs/config.example.yaml config.yaml
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

## 4. 手工启动服务

用显式配置路径启动：

```bash
/opt/syrogo/bin/syrogo -config /opt/syrogo/config/config.yaml
```

如果是在服务器上做临时排查，也可以短时间开启开发日志：

```bash
/opt/syrogo/bin/syrogo -config /opt/syrogo/config/config.yaml -dev-log
```

---

## 5. 验证健康状态与路由

先检查健康状态：

```bash
curl http://127.0.0.1:8080/healthz
```

然后再验证你实际暴露的协议入口之一。

建议优先验证：
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

如果只想先走最小 smoke test，可以先把某条 route 指向 `mock` outbound。

---

## 6. 使用 systemd 托管

示例 unit 文件：

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

保存到：

```text
/etc/systemd/system/syrogo.service
```

然后启用并启动：

```bash
sudo systemctl daemon-reload
sudo systemctl enable syrogo
sudo systemctl start syrogo
```

常用命令：

```bash
sudo systemctl status syrogo
sudo journalctl -u syrogo -f
sudo systemctl restart syrogo
```

---

## 7. 升级流程

一个最小升级流程如下：

1. 下载新的 release 压缩包
2. 解压出新的 `syrogo` 二进制
3. 替换 `/opt/syrogo/bin/syrogo`
4. 保留现有配置文件
5. 重启服务
6. 验证 `/healthz` 和一条真实协议请求

示例：

```bash
sudo systemctl stop syrogo
sudo cp syrogo /opt/syrogo/bin/syrogo
sudo chmod +x /opt/syrogo/bin/syrogo
sudo systemctl start syrogo
```

---

## 8. 反向代理与网络说明

Syrogo 可以直接暴露，也可以放在反向代理后面。

对 `v0.1.x`，建议保持简单：
- 先监听内网端口
- 需要时再通过 nginx 或其他网关对外暴露
- 只对受信任客户端发放 token
- 不要在常规生产流量上长期开启重调试模式

如果前面有反向代理，要确保转发路径和监听端口与你配置的 inbound 路径一致。

---

## 9. 常见排障

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
- Docker 镜像
- Kubernetes manifests
- Helm charts
- Homebrew / apt 包
- 签名、公证与 SBOM 流程

当前目标是先提供一条小而清晰、容易验证、容易维护的二进制部署路径。
