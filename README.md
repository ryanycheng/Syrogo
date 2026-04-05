# Syrogo

Syrogo 是一个多模型 AI Gateway / Semantic Router。

当前仓库处于 0→1 启动阶段，第一版目标是先搭出一个可运行的 Go 服务骨架，包含：
- 基础 HTTP 服务
- 配置加载
- 健康检查
- 最小 `chat/completions` 入口
- router / provider 抽象边界

## 运行

```bash
make run
```

或：

```bash
go run ./cmd/syrogo -config ./configs/config.example.yaml
```

## 当前范围

第一版优先建立服务骨架，不在本阶段引入复杂插件系统、多协议接入或完整 semantic routing。
