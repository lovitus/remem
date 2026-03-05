# remem-guard

Cross-platform memory guard for macOS and Windows.

`remem-guard` monitors process memory in real time, kills runaway child processes before they freeze the whole desktop, and keeps logs in memory only.

## 设计目标

- 防止 AI IDE / 浏览器子进程内存暴涨拖垮系统。
- 优先保活主界面，只终止异常子进程。
- 扫描器绝不重入，避免 shell 任务堆积。
- 仅内存日志，不落盘。

## 核心规则

### 1) 常规命令硬阈值（默认 2 GiB）

命中命令名单（包含 `sed`, `awk`, `grep`, `rg`, `vi`, `vim`, `nano`, `less`, `more`, `python`, `node` 等）且 RSS 超限时，立即终止该进程。

### 2) 应用进程组阈值（默认 6 GiB）

按应用组聚合整棵进程树内存。组超限后，终止组内最大子进程（尽量保留 root UI 进程）：

- codex
- windsurf
- vscode
- antigravity
- chrome
- firefox
- edge
- safari

### 3) 防重入

若上一轮扫描尚未结束，下一轮会跳过，不会并发堆积。

## 托盘与日志

- 托盘菜单：`Open Live Logs`, `Force Scan Now`, `Quit`
- 实时日志页：本地 HTTP 页面
- 日志仅保存在内存环形缓冲区

## 快速开始

```bash
go run ./cmd/remem-guard
```

## 构建

```bash
# macOS
go build -o remem-guard ./cmd/remem-guard

# Windows (native Windows host recommended)
go build -o remem-guard.exe ./cmd/remem-guard
```

也可使用发布打包脚本：

```bash
bash ./scripts/release.sh v0.1.0
```

## 配置（环境变量）

- `REMEM_SCAN_INTERVAL_MS`：扫描周期，默认 `2000`
- `REMEM_COMMAND_LIMIT_GB`：命令单进程阈值，默认 `2`
- `REMEM_GROUP_LIMIT_GB`：应用组阈值，默认 `6`
- `REMEM_MAX_LOG_LINES`：内存日志行数，默认 `400`
- `REMEM_LOG_HTTP_ADDR`：日志 HTTP 监听地址，默认 `127.0.0.1:0`
- `REMEM_EXTRA_COMMANDS`：额外命令列表，逗号分隔

## 运行建议

- 建议开机自启并常驻托盘。
- 首次运行建议在有管理员权限的终端启动，以便有权限终止高权限子进程。
- 浏览器场景下，命中的通常是最异常的 tab/render 进程。

## 文档

- [运行与部署](/Users/fanli/Documents/remem/docs/DEPLOY.md)
- [发布流程](/Users/fanli/Documents/remem/docs/RELEASE.md)
- [变更记录](/Users/fanli/Documents/remem/CHANGELOG.md)
