// readonly.go 只读 bash（bash_readonly）：调研 Agent 的执行面。
//
// 为什么需要（2026-12 用户反馈）：调研 Agent 的白名单里没有 bash，于是它读不了
// 本地数据——查 SQLite 聚合统计跑不了 `sqlite3`、看历史跑不了 `git log`、文件
// 规模统计跑不了 `wc`，只能如实说「做不到」。而直接给它完整 bash 又等于把
// rm -rf / git push 一起交出去。
//
// 方案（用户评估后取「复用 bash 实现 + 命令级白名单校验」）：新增一个独立的
// **只读 bash 工具** `bash_readonly`，与 bash 共用同一个执行核（超时/进程树取消/
// 编码归一/输出截断），只在 Exec 前多做一道命令级校验：
//
//   - 判据是**白名单**（已知只读命令 + 少量命令的子命令/参数白名单），不是黑名单：
//     黑名单永远漏（`find -delete`、`git -c alias.x='!rm' x`、`sqlite3 .shell`），
//     而只读面的需求是收敛的（读文件/检索/查库/看历史）。
//   - **宁保守勿放过**：管道、子 shell、命令替换、重定向、后台任务、解释器一律拒绝
//     ——它们的实际行为无法静态判定。被拒时回填「为什么被拒 + 该派给谁」，
//     模型能据此换做法或如实上报，而不是反复重试。
//   - 判定只做一次（Exec 前），执行时不再二次猜。
//
// 为什么是独立工具而不是「strict 档把 bash 降级」：strict 的契约是「变更类工具
// 直接拒绝」（UI 文案：只读取和搜索，不改文件、不执行命令），把 bash 在 strict 下
// 悄悄变成只读会改掉这条已定语义；而独立工具的权限面写在 Agent 白名单里、写在
// 提示词的工具清单里，模型与用户都看得见。
package tools

import (
	"path/filepath"
	"regexp"
	"strings"
)

// readOnlyRejectHint 是被拒时给模型的出路（拒绝不是死路，模型要知道下一步）。
const readOnlyRejectHint = "本 Agent 是只读执行面：只允许读取类命令（ls / cat / grep / find / wc / git log / sqlite3 查询等）。" +
	"如需修改文件或执行构建/测试，请把这一步交回主 Agent 派给代码 Agent（coder）或测试 Agent（tester），并在结论里说明需要什么改动。"

// envAssignRe 匹配命令前缀的环境变量赋值（`FOO=1 cmd` 里的 `FOO=1`）。
var envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// readOnlyCommands 是只读命令白名单（**不在表里的一律拒绝**）。
//
// 白名单而非黑名单的理由见文件头：只读面的需求收敛，而写操作的入口发散。
// 表里出现名字只说明「这个命令允许被调用」，参数层面的额外限制由
// readOnlySegmentViolation 逐个补（find/sort/git/sqlite3 都有自己的守卫）。
var readOnlyCommands = map[string]bool{
	// 文件与目录（只读）
	"ls": true, "dir": true, "cat": true, "head": true, "tail": true, "wc": true,
	"stat": true, "file": true, "tree": true, "du": true, "df": true, "pwd": true,
	"realpath": true, "basename": true, "dirname": true, "readlink": true,
	// 检索与文本处理（只读）
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "find": true,
	"sort": true, "uniq": true, "cut": true, "tr": true, "comm": true, "join": true,
	"paste": true, "nl": true, "tac": true, "rev": true, "fold": true, "column": true,
	"diff": true, "cmp": true, "strings": true, "od": true, "xxd": true, "hexdump": true,
	"jq": true, "base64": true, "md5sum": true, "sha1sum": true, "sha256sum": true, "cksum": true,
	// 环境与系统信息（只读）
	"echo": true, "printf": true, "which": true, "whereis": true,
	"date": true, "uname": true, "whoami": true, "id": true, "hostname": true,
	"env": true, "printenv": true, "seq": true, "true": true,
	// 数据库与版本库（读查询，参数级守卫见下）
	"sqlite3": true, "git": true,
	// Windows 侧的只读等价物（type 不列：它在 bash 里是内建、在 cmd 里是 cat，
	// 语义随 shell 变，宁可不放行）
	"findstr": true, "where": true,
}

// readOnlyDeniedCommands 给出常见写操作命令的**拒绝理由**（比「不在白名单」更有用：
// 模型知道被拒的是哪一类操作，能立刻换做法）。
var readOnlyDeniedCommands = map[string]string{
	"rm": "删除文件", "rmdir": "删除目录", "del": "删除文件", "erase": "删除文件",
	"mv": "移动/重命名", "move": "移动/重命名", "cp": "复制（可能覆盖）", "copy": "复制（可能覆盖）",
	"mkdir": "创建目录", "md": "创建目录", "touch": "创建/修改文件时间戳",
	"chmod": "修改权限", "chown": "修改属主", "chgrp": "修改属组", "attrib": "修改文件属性",
	"ln": "创建链接", "truncate": "截断文件", "dd": "原始写盘", "tee": "写文件",
	"shred": "销毁文件", "install": "安装/写文件", "mkfifo": "创建管道文件", "mknod": "创建设备节点",
	"sudo": "提权执行", "su": "切换用户", "doas": "提权执行", "runas": "提权执行",
	"kill": "终止进程", "killall": "终止进程", "pkill": "终止进程", "taskkill": "终止进程",
	"shutdown": "关机", "reboot": "重启", "poweroff": "关机", "halt": "关机",
	"systemctl": "服务管理", "service": "服务管理", "sc": "服务管理", "net": "系统/网络配置",
	"reg": "改注册表", "regedit": "改注册表", "schtasks": "计划任务", "crontab": "计划任务", "at": "计划任务",
	"tar": "打包/解包", "zip": "压缩/解压", "unzip": "解压", "gzip": "压缩/解压", "gunzip": "解压",
	"7z": "压缩/解压", "xz": "压缩/解压",
	"curl": "下载（可能写文件/发写请求）", "wget": "下载（会写文件）",
	"ssh": "远程执行", "scp": "远程传输", "rsync": "同步（会写目标）", "sftp": "远程传输", "ftp": "远程传输",
	"eval": "动态执行", "exec": "动态执行", "source": "执行脚本", "xargs": "间接执行命令",
	"nohup": "后台常驻", "watch": "重复执行", "timeout": "间接执行命令",
	"sh": "解释器（脚本内容无法静态判定）", "bash": "解释器（脚本内容无法静态判定）",
	"zsh": "解释器", "dash": "解释器", "ksh": "解释器", "fish": "解释器", "csh": "解释器", "tcsh": "解释器",
	"cmd": "解释器（脚本内容无法静态判定）", "powershell": "解释器（脚本内容无法静态判定）",
	"pwsh":   "解释器（脚本内容无法静态判定）",
	"python": "解释器（脚本内容无法静态判定）", "python3": "解释器（脚本内容无法静态判定）",
	"py": "解释器", "perl": "解释器", "ruby": "解释器", "node": "解释器", "deno": "解释器",
	"bun": "解释器", "php": "解释器", "lua": "解释器",
	"awk": "文本处理但可执行任意写/系统调用", "gawk": "文本处理但可执行任意写/系统调用",
	"mawk": "文本处理但可执行任意写/系统调用", "sed": "文本处理但可原地改写文件（-i）",
	// 包管理与构建（写依赖树/产物，属于代码与测试 Agent 的活）
	"npm": "包管理", "npx": "包管理/执行任意包", "pnpm": "包管理", "yarn": "包管理",
	"pip": "包管理", "pip3": "包管理", "pipx": "包管理", "poetry": "包管理", "uv": "包管理",
	"brew": "包管理", "apt": "包管理", "apt-get": "包管理", "yum": "包管理", "dnf": "包管理",
	"pacman": "包管理", "apk": "包管理", "winget": "包管理", "choco": "包管理", "scoop": "包管理",
	"cargo": "构建/包管理", "rustup": "工具链管理", "gem": "包管理",
	"go": "构建/测试（属于代码与测试 Agent）", "make": "构建", "cmake": "构建", "ninja": "构建",
	"gradle": "构建", "mvn": "构建", "dotnet": "构建", "msbuild": "构建", "tsc": "编译",
}

// readOnlyViolation 返回拒绝理由（空串 = 放行）。这是 bash_readonly 唯一的判据。
func readOnlyViolation(command string) string {
	segs, reason := scanShellCommand(command)
	if reason != "" {
		return reason
	}
	for _, words := range segs {
		if r := readOnlySegmentViolation(words); r != "" {
			return r
		}
	}
	return ""
}

// readOnlySegmentViolation 判定一条简单命令（已按管道/分号/子 shell 切开）。
func readOnlySegmentViolation(words []string) string {
	// 跳过前缀环境变量赋值（`FOO=1 ls` 的 `FOO=1` 只是环境，不是命令）
	i := 0
	for i < len(words) && envAssignRe.MatchString(words[i]) {
		i++
	}
	if i >= len(words) {
		return "" // 只有环境赋值：没有实际动作
	}
	words = words[i:]
	name := strings.ToLower(filepath.Base(words[0]))
	name = strings.TrimSuffix(name, ".exe")
	args := words[1:]

	if reason, denied := readOnlyDeniedCommands[name]; denied {
		return "命令 " + words[0] + " 是「" + reason + "」类操作，只读模式不允许"
	}
	switch name {
	case "git":
		return gitViolation(args)
	case "sqlite3":
		return sqliteViolation(args)
	case "find":
		return findViolation(args)
	case "sort":
		return sortViolation(args)
	case "env":
		// `env` 不带参数是打印环境；带参数则是「间接执行任意命令」
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				return "env 带参数即间接执行命令（" + a + "），只读模式不允许"
			}
		}
		return ""
	}
	if !readOnlyCommands[name] {
		return "命令 " + words[0] + " 不在只读命令白名单内"
	}
	return ""
}

// findViolation：find 的 -exec/-delete/-fprint 系列都能写或执行。
func findViolation(args []string) string {
	blocked := []string{"-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprintf", "-fls"}
	for _, a := range args {
		for _, b := range blocked {
			if a == b {
				return "find 的 " + b + " 会执行命令/删除或写文件，只读模式不允许"
			}
		}
	}
	return ""
}

// sortViolation：sort -o 直接写文件。
func sortViolation(args []string) string {
	for _, a := range args {
		if a == "-o" || strings.HasPrefix(a, "--output") {
			return "sort 的 -o/--output 会写文件，只读模式不允许"
		}
	}
	return ""
}

// gitReadOnlySubcommands 是 git 的只读子命令白名单（其余一律拒绝——写操作、别名、
// 管道执行都在「其余」里）。
var gitReadOnlySubcommands = map[string]bool{
	"log": true, "show": true, "diff": true, "status": true, "blame": true,
	"rev-parse": true, "rev-list": true, "ls-files": true, "ls-tree": true,
	"cat-file": true, "shortlog": true, "describe": true, "grep": true,
	"branch": true, "tag": true, "remote": true, "config": true, "reflog": true,
	"show-ref": true, "for-each-ref": true, "merge-base": true, "name-rev": true,
	"whatchanged": true, "count-objects": true, "diff-tree": true, "diff-index": true,
	"show-branch": true, "check-ignore": true, "check-attr": true, "symbolic-ref": true,
	"version": true, "help": true,
}

// gitReadValueFlags 是「带一个非开关参数、但本身只读」的 git 开关（branch/tag 的
// 过滤条件）。`git branch <name>` 会建分支，所以只认这些开关后面的参数。
var gitReadValueFlags = map[string]bool{
	"--contains": true, "--no-contains": true, "--merged": true, "--no-merged": true,
	"--points-at": true, "--sort": true, "--format": true, "--list": true, "-l": true,
	"--show-current": false, "--column": true, "--abbrev": true,
}

// gitViolation 判定一条 git 命令。三段：全局选项 → 子命令白名单 → 子命令参数。
func gitViolation(args []string) string {
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			break
		}
		switch {
		case a == "-C" || a == "--git-dir" || a == "--work-tree" || a == "--namespace":
			i += 2 // 带一个目录参数
		case a == "--no-pager" || a == "--version" || a == "--help" || a == "-h":
			i++
		case strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree="):
			i++
		default:
			// -c/--config-env 可注入别名（`-c alias.x='!rm' x`）或分页器，
			// --exec-path 可换掉 git 的可执行目录——都不许。
			return "git 全局选项 " + a + " 不在只读白名单内"
		}
	}
	if i >= len(args) {
		return "" // 只有全局选项（如 git --version）
	}
	sub := args[i]
	rest := args[i+1:]
	if !gitReadOnlySubcommands[sub] {
		return "git " + sub + " 不是只读子命令（写操作/别名/未列入白名单）"
	}
	// 任何位置出现 --output 都是写文件（git log/diff 都支持）
	for _, a := range rest {
		if a == "--output" || strings.HasPrefix(a, "--output=") {
			return "git 的 --output 会写文件，只读模式不允许"
		}
	}
	switch sub {
	case "branch", "tag":
		return gitListRefsViolation(sub, rest)
	case "remote":
		if len(rest) == 0 {
			return ""
		}
		if rest[0] == "show" || rest[0] == "get-url" || strings.HasPrefix(rest[0], "-") {
			return ""
		}
		return "git remote " + rest[0] + " 会修改远端配置，只读模式不允许"
	case "config":
		for _, a := range rest {
			if a == "--list" || a == "-l" || strings.HasPrefix(a, "--get") {
				return ""
			}
		}
		return "git config 只在 --list/-l/--get 下只读；其余写法会写配置"
	case "reflog":
		if len(rest) > 0 && (rest[0] == "expire" || rest[0] == "delete") {
			return "git reflog " + rest[0] + " 会改写 reflog，只读模式不允许"
		}
	}
	return ""
}

// gitListRefsViolation：`git branch <name>` / `git tag <name>` 会创建引用；
// 只有「过滤参数跟在只读开关后面」的形态才放行。
func gitListRefsViolation(sub string, args []string) string {
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if i > 0 && gitReadValueFlags[args[i-1]] {
			continue
		}
		return "git " + sub + " " + a + " 会创建/改写引用，只读模式不允许"
	}
	return ""
}

// sqliteWriteRe 命中 SQL 里的写操作关键字（大小写不敏感）。
var sqliteWriteRe = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|attach|detach|vacuum|reindex|replace|begin|commit|rollback|savepoint|release|pragma)\b`)

// sqliteReadDotCommands 是允许的 sqlite3 点命令（读结构/设置输出格式）。
var sqliteReadDotCommands = map[string]bool{
	".tables": true, ".schema": true, ".indexes": true, ".indices": true, ".mode": true,
	".headers": true, ".width": true, ".databases": true, ".dump": true, ".timeout": true,
	".nullvalue": true, ".separator": true, ".show": true, ".stats": true, ".timer": true,
	".help": true, ".quit": true, ".exit": true,
}

// sqliteViolation 判定 sqlite3 调用：只放行查询（SQL 里不得有写关键字）、
// 只读点命令与 -readonly/-ro 开关。
func sqliteViolation(args []string) string {
	for _, a := range args {
		la := strings.ToLower(a)
		switch {
		case strings.HasPrefix(a, "."):
			if !sqliteReadDotCommands[la] {
				return "sqlite3 点命令 " + a + " 会写库或执行外部命令（只允许 " +
					".tables/.schema/.indexes/.mode/.headers/.dump 等只读命令）"
			}
		case la == "-cmd" || la == "-init" || strings.HasPrefix(la, "-init"):
			return "sqlite3 的 " + a + " 会执行启动脚本/附加 SQL，只读模式不允许"
		case strings.HasPrefix(a, "-"):
			// -readonly/-ro/-header/-json/-csv 等开关都只影响读的方式
			continue
		default:
			if m := sqliteWriteRe.FindString(a); m != "" {
				return "SQL 里含写操作关键字 " + strings.ToUpper(m) + "，只读模式只允许 SELECT/EXPLAIN 查询"
			}
		}
	}
	return ""
}

// scanShellCommand 把命令切成「简单命令的 token 序列」，并在切分过程中拒绝无法
// 静态判定的结构（重定向、命令替换、子 shell 之外的写入口）。
//
// 为什么要自己扫一遍而不是 strings.Fields：引号里的 `>` / `|` 是普通字符
// （`grep -r "a|b" .`），而引号外的它们才是操作符——直接按空白切会把两者混为一谈，
// 于是要么误放行（`ls; rm -rf x` 被当成一条命令）要么误拒绝。
func scanShellCommand(command string) ([][]string, string) {
	var (
		segs    [][]string
		words   []string
		word    strings.Builder
		hasWord bool
	)
	flushWord := func() {
		if hasWord {
			words = append(words, word.String())
			word.Reset()
			hasWord = false
		}
	}
	flushSegment := func() {
		flushWord()
		if len(words) > 0 {
			segs = append(segs, words)
			words = nil
		}
	}

	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch c {
		case '\'':
			hasWord = true
			for i++; i < len(runes); i++ {
				if runes[i] == '\'' {
					break
				}
				word.WriteRune(runes[i])
			}
		case '"':
			hasWord = true
			for i++; i < len(runes); i++ {
				if runes[i] == '\\' && i+1 < len(runes) {
					i++
					word.WriteRune(runes[i])
					continue
				}
				if runes[i] == '"' {
					break
				}
				if runes[i] == '$' && i+1 < len(runes) && runes[i+1] == '(' {
					return nil, "命令替换 $( ) 的内容无法静态判定，只读模式不允许"
				}
				if runes[i] == '`' {
					return nil, "反引号命令替换的内容无法静态判定，只读模式不允许"
				}
				word.WriteRune(runes[i])
			}
		case '\\':
			if i+1 < len(runes) {
				i++
				word.WriteRune(runes[i])
				hasWord = true
			}
		case ' ', '\t', '\r':
			flushWord()
		case '\n':
			flushSegment()
		case ';':
			flushSegment()
		case '&':
			if i+1 < len(runes) && runes[i+1] == '&' {
				flushSegment()
				i++
				continue
			}
			flushSegment() // 单个 & = 后台执行
		case '|':
			if i+1 < len(runes) && runes[i+1] == '|' {
				flushSegment()
				i++
				continue
			}
			flushSegment()
		case '(', ')':
			flushSegment() // 子 shell / 分组：切开后逐段判定
		case '<', '>':
			return nil, "重定向（" + string(c) + "）会写文件或读非预期输入，只读模式不允许"
		case '`':
			return nil, "反引号命令替换的内容无法静态判定，只读模式不允许"
		case '$':
			if i+1 < len(runes) && runes[i+1] == '(' {
				return nil, "命令替换 $( ) 的内容无法静态判定，只读模式不允许"
			}
			word.WriteRune(c)
			hasWord = true
		case '#':
			if !hasWord {
				// 注释到行尾（引号内的 # 已在上面的分支里被当作普通字符）
				for i < len(runes) && runes[i] != '\n' {
					i++
				}
				flushSegment()
			} else {
				word.WriteRune(c)
			}
		default:
			word.WriteRune(c)
			hasWord = true
		}
	}
	flushSegment()
	if len(segs) == 0 {
		return nil, "命令为空"
	}
	return segs, ""
}
