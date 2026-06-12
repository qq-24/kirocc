# kirocc 本地部署指南

## 前提条件

- Kiro CLI 2.6.1+ 已安装并登录（`C:\Users\mingh\AppData\Local\Kiro-Cli\`）
- Go 1.26+（`D:\go126\go\bin\go.exe`）
- Clash TUN 模式开启（不需要设代理环境变量）

## 编译

```bash
cd src
GOPATH=D:/go_path GOEXPERIMENT=jsonv2 D:/go126/go/bin/go.exe build -o ../bin/kirocc.exe ./cmd/kirocc
```

## 启动

```powershell
.\start.ps1
```

开机自启已配置：`%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\kirocc.bat`

## Claude Code 配置

`C:\Users\mingh\.claude\settings.json` 关键字段：
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3456",
    "ANTHROPIC_API_KEY": "not-used"
  },
  "model": "claude-opus-4-6-20250414[1m]"
}
```

切回 DeepSeek：`cp D:\mingh\Documents\kirocc-orig\settings.json.bak C:\Users\mingh\.claude\settings.json`

## 关键发现（2026-06-12 排查记录）

### Thinking 支持

| 条件 | 效果 |
|---|---|
| `thinking:{type:"adaptive"}` + UA `appVersion-2.6.1` | 后端返回 `reasoningContentEvent` ✅ |
| 不传 thinking 字段 | 无推理事件 |
| UA `appVersion-2.0.0`（旧版） | 无推理事件（即使传了 thinking 字段） |

**UA 版本是触发 thinking 的关键。** 后端按 UA 决定是否返回推理事件。

### Effort 效果

| effort | 行为 |
|---|---|
| 不设 | 正常输出 |
| low | 简短输出 |
| max | 更详细输出，配合 thinking:adaptive 有真正的推理延迟（+3-5秒 TTFT） |

### 端点

- 旧端点 `q.{region}.amazonaws.com`：kiro-cli ≤2.0 使用
- 新端点 `runtime.{region}.kiro.dev`：kiro-cli 2.5.1+ 使用
- Region 映射：`eu-west-1` → `eu-central-1`（凭证 region 是 eu-west-1 但实际要连 eu-central-1）

### 网络

- Clash TUN 模式下直连即可，不设 HTTPS_PROXY/HTTP_PROXY
- kirocc 本身是本地代理，不需要外部代理转发

### 踩坑记录

1. `runtime.eu-west-1.kiro.dev` 不通 → 需要 region 映射到 `eu-central-1`
2. 旧 UA（2.0.0）→ 后端不返回 thinking 事件
3. 端口 3456 占用 → start.ps1 按端口杀旧进程
4. `KIROCC_API_KEY` 环境变量如果非空会开启认证 → 确保为空
