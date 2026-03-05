# remem

Cross-platform memory guard for macOS and Windows.

`remem` monitors process memory in real time, terminates runaway child processes before they freeze the desktop, and keeps logs in memory only.

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

### 3) 非重入扫描

扫描串行执行。上一轮未完成时不会并发堆积任务。

### 4) 托盘与内存日志

- 托盘菜单：`Open Live Logs`, `Force Scan Now`, `Quit`
- 彩色托盘图标（Windows/macOS）
- 日志只在内存环形缓冲区中保存，不写盘

## 快速开始

```bash
go run ./cmd/remem
```

## 构建

```bash
# macOS
go build -o remem ./cmd/remem

# Windows
go build -o remem.exe ./cmd/remem
```

发布包构建（Windows 自动无黑框）：

```bash
bash ./scripts/release.sh v0.2.0
```

## 配置

### 基础环境变量

- `REMEM_SCAN_INTERVAL_MS`：扫描周期（默认 macOS 2000，Windows 3000）
- `REMEM_COMMAND_LIMIT_GB`：命令单进程阈值（默认 `2`）
- `REMEM_GROUP_LIMIT_GB`：应用组阈值（默认 `6`）
- `REMEM_MAX_LOG_LINES`：内存日志行数（默认 `400`）
- `REMEM_LOG_HTTP_ADDR`：日志 HTTP 监听地址（默认 `127.0.0.1:0`）
- `REMEM_SHOW_CONSOLE`：Windows 设为 `1` 可保留控制台

### 自定义命令名 / 程序名

- `REMEM_EXTRA_COMMANDS`：追加命令名单（逗号分隔）
- `REMEM_REMOVE_COMMANDS`：从默认命令名单移除（逗号分隔）
- `REMEM_EXTRA_GROUPS`：追加程序组名（逗号分隔）
- `REMEM_REMOVE_GROUPS`：移除默认程序组名（逗号分隔）

示例：

```bash
REMEM_EXTRA_COMMANDS="deno,bun"
REMEM_EXTRA_GROUPS="brave,opera"
```

### JSON 配置文件（可选）

设置 `REMEM_CONFIG_PATH=/path/to/config.json`，格式见 `docs/config.example.json`。

## 文档

- `docs/DEPLOY.md`
- `docs/RELEASE.md`
- `CHANGELOG.md`
