# Windows 下 bash 工具的现状、问题与方案（2026-12 调研）

> 取证来源：`internal/tools/bash.go` 源码 + 本机真库（`%APPDATA%\lxcode\sessions\sessions.db`，
> 1377 条会话 / 122281 条消息 / 66193 条工具结果）+ 本机实测（Windows 11 26200，
> 代码页 936，PowerShell 5.1.26100，未装 pwsh）。

## 1. 现状（代码取证）

### 1.1 用什么执行

`selectShell()`（`internal/tools/bash.go:37`）在 `bashDef()` 构造时**调用一次并固化**，
Windows 下的候选顺序：

| 顺序 | 候选 | 说明 |
|---|---|---|
| 1 | `LookPath("sh.exe")` | Git Bash 提供（`C:\Program Files\Git\usr\bin\sh.exe`），无 WSL stub 问题 |
| 2 | `LookPath("bash.exe")` 且**不在 %SystemRoot% 下** | 排除 `System32\bash.exe`（WSL 启动 stub） |
| 3 | `C:\Program Files\Git\bin\bash.exe` / `(x86)` | 兜底：PATH 里没有 Git 时仍能找到默认安装 |
| 4 | `cmd` + `/c` | 最后退路 |

Unix：`sh -c`。

### 1.2 「默认是 WSL 吗？」——**不是**

- 代码里**没有任何** WSL 分支，也没有 `wsl.exe` 候选；`isUnderSystemDir` 存在的唯一目的
  就是**排除** WSL stub（`internal/tools/bash.go:60`）。
- 本机实测 `selectShell()` 落在 `C:\Program Files\Git\usr\bin\sh.exe`；
  `LookPath("bash.exe")` 同样落在 Git 目录（本机 `System32\bash.exe` 已不存在，
  只有 `System32\wsl.exe`）。
- 用户看到「像 WSL」的形态来自 **Git Bash 的 MSYS 层**（见 §2.2），不是 WSL：
  `/c/Users/...` 路径、`/usr/bin/bash: line 1: xxx: command not found` 都是 MSYS 的措辞。
- 真库里 `wsl` 字样出现 78 次，全是**会话内容/输出文本**提到，不是执行路径。

### 1.3 配置项

**没有**任何 shell 配置项（`internal/config` 里搜不到 shell 相关字段），也没有
`LXCODE_SHELL` 环境变量——shell 选择完全由 PATH 探测决定。这意味着：

- 装了 pwsh 也**不会**被用到（当前代码不认它）；
- 服务形态（SCM，只有机器级 PATH）下，只要 Git 装在默认路径，第 3 步仍能兜住；
  Git 不在默认路径时静默退到 `cmd /c`，模型写的 bash 语法全部失效。

### 1.4 真实失败统计（真库）

| 指标 | 数值 |
|---|---|
| bash 调用总数 | 34335 |
| 非零退出码 | 1616（4.7%） |
| 失败后**紧接着又发一条 bash** | 1530 / 1616（94.7%——几乎每次失败都在试错） |
| 工具结果含非法 UTF-8 | 431 / 66193（bash 354、search 74、read_file 2、session_search 1） |
| bash 结果含 U+FFFD（乱码） | 57，其中 **56/57 紧接着又发了一条 bash** |
| `ERROR: Invalid argument/option - 'C:/Program Files/Git/FI'` | 12（MSYS 路径改写） |
| `/usr/bin/bash.<X>`（`$_` 被 sh 展开） | 37 |
| 命令里显式写 `powershell` | 519 次，失败 58 次、输出乱码 19 次 |
| 命令里显式写 `pwsh` | 1 次（且是误匹配的文本，不是真调用） |

退出码分布：`1` ×1159、`2` ×268、`255` ×49、`127` ×43（命令不存在）、`128` ×22。

## 2. 问题分析

### 2.1 PowerShell 版本：本机只有 5.1，模型常按 7 写

- 本机 `powershell -Command '$PSVersionTable.PSVersion'` → `5.1.26100.9711`；
  `LookPath("pwsh.exe")` → 不存在。
- 真库里 `&&` 在 PS 5.1 上的失败只有 1 例（`The token '&&' is not a valid statement
  separator`），**说明模型已经很少直接写 PS 脚本**——它更常写 bash，偶尔用
  `powershell -Command "..."` 查进程/端口。所以 PS 版本兼容目前**不是主要矛盾**，
  主要矛盾是下面两条（MSYS 路径改写 + `$_` 展开）与编码。

### 2.2 Git Bash（MSYS）的两类高频坑（真库取证，占比最高）

**① 斜杠参数被当路径改写**（12 例）：

```
$ tasklist /FI "PID eq 33168"
ERROR: Invalid argument/option - 'C:/Program Files/Git/FI'.
```

MSYS 会把以 `/` 开头、看起来像 Unix 路径的参数改写成 `C:/Program Files/Git/<x>`。
受害命令：`tasklist /FI`、`taskkill /PID`、`cmd /c`（`/c` 被改成 `C:/Program Files/Git/c`，
于是 `cmd` 直接进入交互模式、把后续内容当输入吃掉——真库 3 例）。
处置：`//FI`（双斜杠）或 `MSYS_NO_PATHCONV=1` 前缀。**已写进工具描述**（`shellSyntaxHint`）。

**② `$_` / `$( )` 被 sh 先展开**（37 例）：

```
$ powershell -NoProfile -Command "Get-Process | Where-Object { $_.ProcessName -like '*lxcode*' }"
/usr/bin/bash.ProcessName : 无法将“/usr/bin/bash.ProcessName”项识别为 cmdlet ...
```

外层 sh 把 `$_` 换成上一条命令的最后一个参数（`/usr/bin/bash`），PS 收到的表达式是坏的。
处置：`-Command '...'` 用**单引号**包住，或写 `.ps1` 文件再执行。**已写进工具描述**。

### 2.3 编码：中文输出全是 GBK（已修，见 §4.1）

后端是 GUI/服务形态，子进程拿到 `CREATE_NO_WINDOW` 新建的隐藏控制台，代码页仍是 **936**。
`ipconfig` / `tasklist` / `netstat` / `powershell` 的中文输出因此是 GBK 字节，而旧实现
`string(out)` 直接当 UTF-8 拼进工具结果 → JSON 序列化成一串 `����`。

本机实测（`sh -c 'ipconfig'`）：

```
no-chcp     "\r\nWindows IP \xc5\xe4\xd6\xc3\r\n"        ← GBK，非法 UTF-8
chcp 65001  "\r\nWindows IP Configuration\r\n"           ← 转成英文（未装语言包时的降级）
powershell 无 chcp → "\xd6\xd0\xceĲ\xe2\xca\xd4"（GBK「中文测试」）
powershell 有 chcp → "中文测试"（UTF-8）
```

### 2.4 「检测并选择 pwsh → powershell → cmd」是否值得

不建议把**默认 shell** 换成 PowerShell：

- 模型对 bash 语法最熟（训练语料如此），换 PS 会让绝大多数命令重写；
- 真库里模型自发写 `powershell -Command` 的只有 519/34335（1.5%），说明它并不指望
  PS 当主 shell；
- Git Bash 提供的 POSIX 工具（`grep`/`sed`/`ls`/`find`）是当前工作流的一部分。

## 3. 方案

### 3.1 shell 选择策略（建议，未实施）

保持现有顺序，只补一条**可配置覆盖**，不引入自动探测 pwsh：

1. 新增 `LXCODE_SHELL` 环境变量（或 `behavior.json` 的 `shell` 字段）：
   值可以是 `cmd` / `powershell` / `pwsh` / 绝对路径；空 = 走现有探测。
   理由：少数用户确实想要 pwsh 语义（跨平台脚本、`&&`、对象管道），但这该是**显式选择**，
   不该由「装没装」决定（自动切会让同一份提示词在不同机器上语义不同）。
2. 探测顺序保持 `sh.exe → 非系统 bash.exe → Git 默认路径 → cmd`。
   **不要把 pwsh/powershell 插进默认链**：它们是「另一个语言的解释器」，不是 bash 的替代品。
3. `shellSyntaxHint` 已按实际选中的 shell 生成（cmd / Git Bash 两套），
   新增 `powershell` 分支时照同一模式补「PS 5.1 不支持 `&&`，用 `;`」这类提示。

### 3.2 提示词/描述补充（已部分实施）

- **已完成**：Git Bash 的两条坑（MSYS 路径改写、`$_` 展开）+ GBK 已自动转码，写进
  `bash` 工具描述（模型每次请求都读得到）。
- **建议追加**（低优先，等有真实失败案例再写）：
  - 「查进程/端口优先用 `netstat -ano` + `tasklist //FI`，别用 PowerShell one-liner」；
  - 「需要 PowerShell 时把脚本写成 `.ps1` 再用 `powershell -NoProfile -File` 执行」。
  理由：描述本身进上下文，写太多规则会挤占真正的任务信息——只写**真库证明高频**的。

### 3.3 错误反馈优化（建议，未实施）

`execNote`（`internal/tools/bash.go:233`）目前只回「退出码 N」。建议加一层**壳级**归因：

| 输出特征 | 附加说明 |
|---|---|
| `command not found` / `不是内部或外部命令`（127） | 「命令不在 PATH（当前 shell: X）；Windows 程序常需绝对路径或 `.\` 前缀」 |
| `Invalid argument/option - 'C:/Program Files/Git/` | 「MSYS 把 `/X` 当路径改写了，用 `//X` 或 `MSYS_NO_PATHCONV=1`」 |
| `不是此版本中的有效语句分隔符` / `valid statement separator` | 「PowerShell 5.1 不支持 `&&`，用 `;` 或分两条执行」 |
| `无法将“/usr/bin/bash.` | 「`$_` 被外层 sh 展开，PS 脚本用单引号包住或写成 .ps1」 |

实现位置建议：`execNote` 旁边加一个纯函数 `shellFailureHint(shellName, output) string`，
只在**非零退出码**时追加一行（不影响成功路径），单测按上面四条特征串各钉一条。
**注意**：这属于「错误信息加提示」，不改变执行语义，风险低；但它会让每条失败结果的
长度增加一行，需要确认 `maxToolResultBytes`（8KB）截断口径不受影响。

### 3.4 是否推荐装 PowerShell 7

**不推荐作为默认**，推荐作为**可选**：

- 收益：`&&`/`||` 可用、跨平台一致的 cmdlet 集、UTF-8 默认（`$OutputEncoding` 与
  `[Console]::OutputEncoding` 默认 UTF-8）、性能更好；
- 代价：多一个 ~100MB 运行时；模型仍会按 bash 写命令，PS 7 并不会让 bash 语法生效；
- 若实施 §3.1 的显式配置，检测逻辑只需一行：`exec.LookPath("pwsh.exe")` 成功则允许
  `shell=pwsh`，失败则报「未安装 pwsh（winget install Microsoft.PowerShell）」并回退到
  默认链——**不要静默回退**（静默回退会让用户以为配置生效了）。

## 4. 已实施的低风险修复

### 4.1 输出编码归一（`internal/tools/encoding*.go`）

- `normalizeConsoleOutput(out []byte) string`：合法 UTF-8 **原样返回**（零回归面），
  非法段**按行**用 `MultiByteToWideChar(CP_ACP)` 解码（中文系统 = GBK，西欧/俄语机器
  自动跟着本机 ANSI 代码页走），失败回落 U+FFFD。
- 接入点：`bash` 前台（`bash.go`）与自定义二进制工具（`custom.go` 的 `runArgv`）。
- **刻意不做** `chcp 65001` 命令前缀：会让确认卡展示的命令与实际执行的不一致，
  且对「无视代码页直接写 GBK」的程序无效。
- 钉子：`internal/tools/encoding_test.go`（合法 UTF-8 逐字节不变 / 任意坏字节输出必为
  合法 UTF-8 / 混合输出逐行处理）。

- 附带修了一处相邻隐患：`bash` / 自定义工具的输出截断（32KB）原来用 `result[:max]`
  直接切字节，会把一个多字节字符劈成两半——产出的又是非法 UTF-8（与本条要修的编码
  问题同一后果）。改为 `truncateUTF8`（退到 rune 边界，最多回退 2 字节）。

**未覆盖**：后台任务输出（`internal/jobs` 的 `Read` 返回 `string(j.buf[...])`）。
它按字节游标增量读取，在**字节层**转码会破坏游标语义（`from`/`total` 是原始字节偏移），
要做得把游标改成「已解码前缀长度」或按行缓冲——属于中等改动，本次不做，记录在此。

### 4.2 Git Bash 语法提示（`shellSyntaxHint`）

真库取证的两条高频坑写进工具描述，并按实际选中的 shell 生成（cmd / Git Bash 两套互斥）。
钉子：`internal/tools/bash_shell_test.go`。
