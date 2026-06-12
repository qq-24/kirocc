# kirocc (fork)

本地代理，将 Anthropic Messages API 请求转发到 Kiro (Amazon Q) 后端。基于 [d-kuro/kirocc](https://github.com/d-kuro/kirocc) v0.4.0。

## 本 fork 的改动

| 改动 | 说明 |
|---|---|
| Thinking 支持 | 传 `thinking:{type:"adaptive"}`，触发后端原生推理（返回 `reasoningContentEvent`） |
| UA 对齐 2.6.1 | 后端按 UA 版本决定是否返回 thinking 事件 |
| 端点 + Region 映射 | `runtime.{region}.kiro.dev` + `eu-west-1→eu-central-1` |
| Windows DB 路径 | `AppData\Local\Kiro-Cli\data.sqlite3` |
| MaxBytesReader 50MB | 支持大图片/长上下文 |
| 历史消息图片 | 多轮对话中的图片不丢弃 |
| thinking→text 空白缓冲 | thinking 块结束后的空白不泄漏到正文 |
| defaultThinkingEffort=max | 默认最大推理深度 |

## 前提

- [Kiro CLI](https://kiro.dev) 2.6.1+ 已安装并登录
- Go 1.26+（需要 `GOEXPERIMENT=jsonv2`）
- Windows + Clash TUN 模式（或其他能访问 `runtime.*.kiro.dev` 的网络）

## 编译

```bash
cd src
GOPATH=D:/go_path GOEXPERIMENT=jsonv2 D:/go126/go/bin/go.exe build -o ../bin/kirocc.exe ./cmd/kirocc
```

## 启动

```powershell
.\start.ps1
```

无窗口后台运行，监听 `http://127.0.0.1:3456`。

## 配合 Claude Code 使用

`~/.claude/settings.json`:
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3456",
    "ANTHROPIC_API_KEY": "not-used"
  },
  "model": "claude-opus-4-6-20250414[1m]"
}
```

## 模型映射

通过 `KIROCC_MODEL_MAPPINGS` 环境变量配置（见 `start.ps1`）。

## 文档

- [docs/DEPLOY.md](docs/DEPLOY.md) — 部署指南 + 踩坑记录
- [上游 README](https://github.com/d-kuro/kirocc) — 完整功能说明
