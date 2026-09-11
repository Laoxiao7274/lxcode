# 02 · Windows 服务运维

> 2026-09-10。后端服务化——对齐参考项目 local-myt-agent 在设备上的运维形态
> （服务托管、开机自启、崩溃自愈、脚本部署/升级/回滚、探针验收），全部落到
> Windows 等价物。形态对齐矩阵见 AGENTS.md §2 / §6。

## 1. 为什么是 Windows 服务（SCM）

参考项目的托管形态是 Alpine OpenRC / Docker（`--restart unless-stopped`）。
Windows 上的原生等价物是 SCM 服务：

| 参考项目 | lxcode Windows |
|---|---|
| OpenRC 服务 / Docker 容器 | `sc create lxcode` |
| `rc-update add default`（开机自启） | `start= auto` |
| `--restart unless-stopped`（崩溃自愈） | `sc failure ... actions= restart/5000/restart/5000/restart/60000` |
| `/mmc/myt-agent/` 固定目录 | `%ProgramData%\lxcode\` |
| `/var/log/myt-agent.log` | `logs\lxcode.log`（16MB 轮转 ×3） |
| 30s 热加载 + model.changed | 同款（`reloadLoop`） |
| deploy/update/rollback 脚本 | `scripts/service/*.ps1` |
| wsprobe 验收 | `lxcode --probe`（内置） |

## 2. 布局与配置解析

```
%ProgramData%\lxcode\
├── bin\lxcode.exe      # 二进制（sc binPath 指向这里，显式 --config）
├── config\models.json       # 配置（服务与 CLI 共享同一份）
├── sessions\                # 会话存储（从 config 路径推导）
└── logs\lxcode.log     # 日志（.1/.2/.3 轮转）
```

配置解析顺序（`cmd/lxcode/serve.go:resolveConfigPath`）：

1. `--config` 显式指定
2. `LXCODE_CONFIG` 环境变量
3. `.\config\models.json`（存在时——开发仓库形态）
4. `%ProgramData%\lxcode\config\models.json`（安装形态）

3→4 的内置回退等价于参考项目的 `/usr/local/bin/myt-agent` 包装器（它用包装器
强制 `MYT_AGENT_CONFIG` 解决配置分叉；我们把规则做进解析顺序）。服务安装时
binPath 显式带 `--config`，服务侧不存在分叉可能。

## 3. 服务生命周期

- **启动**：SCM → `svc.Run` → `Execute`（StartPending → 装配 runServe → Running）。
  日志先行落文件（服务进程 stdout 是无效句柄）。
- **停止**：SCM Stop → ctx 取消 → `closeAllClients()`（强制断 WS——
  `http.Shutdown` 只关监听不打断长连接，WS 读循环会挂着）→ Shutdown 等收尾
  → 进程退出。SCM 侧 WaitHint 15s。
- **崩溃**：SCM failure recovery 自动重启（5s/5s/60s，24h 重置计数）。
- **空配置不拒绝启动**：服务形态崩溃重启循环比明确报错更糟——启动时记警告，
  `chat.send` 按请求返回 CodeNoDefaultModel；手改配置 30s 内热加载接上。

## 4. 脚本（管理员 PowerShell，仓库根运行）

| 脚本 | 作用 |
|---|---|
| `scripts\service\install.ps1` | 安装：端口预检（被占硬拦，防启动→失败→重启循环）→ 布置目录 → 预置/生成配置 → `sc create`（自启+自愈）→ 起服务 → `--probe` 验收 → 失败自动回退 |
| `scripts\service\update.ps1` | 升级：可选 `-Sha256` 校验 → 停（Wait-Stopped 等真正停下——exe 被占用替换会失败）→ 备份 `.bak` → 替换 → 起 → 验收 → 失败自动回滚 |
| `scripts\service\uninstall.ps1` | 卸载（保留 config/sessions/logs） |

退出码约定：`--probe` 0=全好；1=协议失败（脚本回退/回滚）；2=活着但没配模型
（安装算成功，提示用户填配置）。

## 5. 已验证 / 待真机验证

- **已验证**：`go test ./...` 全绿（serve 生命周期/优雅退出不挂、热加载广播、
  配置解析优先级）；控制台模式 `--serve` + `--probe` 全过（连接/ready/hello/
  model.list/session.list/history）；脚本语法解析通过。
- **待管理员环境验证**（本开发会话无管理员权限，用户一条命令即可）：
  `scripts\service\install.ps1 -ConfigPath .\config\local.json` → 观察 probe
  输出 → `sc qc lxcode` 核对 binPath → 重启机器验证开机自启（可选）。
