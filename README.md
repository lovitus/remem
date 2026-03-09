# remem

Cross-platform memory guard for macOS and Windows.

`remem` monitors process memory in real time, terminates runaway child processes before they freeze the desktop, and keeps logs in memory only.

## 核心规则

### 1) 常规命令硬阈值（默认 2 GiB）

命中命令名单（包含 `sed`, `awk`, `grep`, `rg`, `vi`, `vim`, `nano`, `less`, `more`, `python`, `node` 等）且 RSS 超限时，立即终止该进程。

### 2) 应用进程组阈值（默认 6 GiB）

按应用组聚合进程树内存。组超限后，终止组内最大子进程（尽量保留 root UI 进程）：

- codex
- windsurf
- vscode
- antigravity
- chrome
- firefox
- edge
- safari

### 3) 低开销策略

- 扫描串行执行，不重入。
- 只对相关进程查 RSS，不对所有进程全量取内存。
- Windows 默认启用分组轮询（`REMEM_GROUPS_PER_SCAN=2`），并对接近阈值的热组提频扫描。

### 4) 托盘与内存日志

- 托盘菜单：`Open Live Logs`, `Edit Rules`, `Force Scan Now`, `Quit`
- `Edit Rules` 保存后立即生效，并持久化到规则文件。
- 日志只在内存中保存，不写盘：
  - `Routine Scan Logs` 仅保留最近 10 行（可通过 `REMEM_ROUTINE_LOG_LINES` 调整）
  - `Important Logs`（action/error/kill）保留最近 100 行（可通过 `REMEM_IMPORTANT_LOG_LINES` 调整）

## 快速开始

```bash
go run ./cmd/remem
```

## macOS 安装（DMG）

- 在 Release 下载 `remem-macos-arm64-v*.dmg`
- 双击打开后，把 `remem.app` 拖到 `Applications`
- 首次被拦截时可执行：

```bash
xattr -dr com.apple.quarantine "/Applications/remem.app"
```

- 然后去 `System Settings -> Privacy & Security` 点击 `Open Anyway`

## 构建

```bash
# macOS
go build -o remem ./cmd/remem

# Windows
go build -o remem.exe ./cmd/remem
```

发布包构建（Windows 自动无黑框）：

```bash
bash ./scripts/release.sh v0.3.9
```

## 配置

### 基础环境变量

- `REMEM_SCAN_INTERVAL_MS`：扫描周期（默认 macOS `2000`，Windows `3000`）
- `REMEM_COMMAND_LIMIT_GB`：命令单进程阈值（默认 `2`）
- `REMEM_GROUP_LIMIT_GB`：应用组阈值（默认 `6`）
- `REMEM_GROUPS_PER_SCAN`：每轮扫描的组数（默认 macOS `0=全部`，Windows `2`）
- `REMEM_GROUP_HOT_RATIO`：热组阈值比例（默认 `0.70`）
- `REMEM_GROUP_HOT_TTL_SEC`：热组持续提频时长（默认 `30`）
- `REMEM_ROUTINE_LOG_LINES`：Routine 日志行数（默认 `10`）
- `REMEM_IMPORTANT_LOG_LINES`：Important 日志行数（默认 `100`）
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

默认规则文件路径：

- macOS: `~/Library/Application Support/remem/rules.json`
- Windows: `%AppData%\\remem\\rules.json`

也可通过 `REMEM_CONFIG_PATH` 指定。

支持的关键字段：

- `limits.commandGiB` / `limits.groupGiB`：覆盖全局命令/程序组上限
- `commands.limitsGiB.<name>`：给单个命令设置上限（GiB）
- `groups.limitsGiB.<name>`：给单个程序组设置上限（GiB）

格式参考：`docs/config.example.json`

## 文档

- `docs/DEPLOY.md`
- `docs/RELEASE.md`
- `CHANGELOG.md`
