// sshhosts.go — ssh_hosts：管理的 SSH 主机别名 + 命令执行 + 探活缓存（P8-2）。
//
// 为什么需要（2026-12 真库数据）：bash 里 ssh/scp 1,607 次主操作、平均 32.6s、
// 208 次 ≥60s——全部是设备运维形态的重复调用（`sshpass -p <明文密码> ssh
// -o StrictHostKeyChecking=no user@host ...` 每条重放）。轻量版三件套：
//
//   1. **别名**：hosts.json 里 [{alias, host, port, user, password?, key_path?,
//      hostkey?}]，模型只写别名 + 命令——用户名/端口/凭据不再逐条拼进命令；
//   2. **hostkey 声明制**：hostkey 声明了就传
//      `-o UserKnownHostsFile=<tmp> -o StrictHostKeyChecking=yes`
//      （每条命令零信任重建 known_hosts，只认配置里那一个指纹）——替代每条
//      命令的 no 检查（TOFU 全放行 = MITM 静默成功）；没声明 = `accept-new`
//      （首次接受、之后校验，OpenSSH 7.6+ 语义）；
//   3. **探活缓存**：同一 host 60s 内成功连接过就跳过连接测试（本工具自管，
//      与 ssh 自身的 ControlMaster 无关——不写用户 ~/.ssh/config）。
//
// 凭据纪律：密码**支持 env 引用**（`${VAR}` 形式，读取时展开），避免明文落盘；
// 密码认证第一版只在 sshpass 存在时可用（OpenSSH 的 -o 不支持传密码）——
// 无 sshpass 时报人话错误提示配 key_path。默认白名单**不含**本工具：
// 远程执行 = 高危确认门，由用户在 Agent 编辑器显式勾选。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// sshHostsToolName 是 ssh_hosts 工具名（种子目录引用同一字面量）。
const sshHostsToolName = "ssh_hosts"

// 探活缓存与超时参数。
const (
	sshAliveTTL     = 60 * time.Second // 探活缓存有效期
	sshDefaultTo    = 30 * time.Second
	sshMaxTo        = 300 * time.Second
	sshConnectProbe = 10 * time.Second // 探活（连接测试）的超时
)

// SSHHost 是 hosts.json 里的单条主机条目。
type SSHHost struct {
	Alias    string `json:"alias"`
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"` // 支持 ${ENV_VAR} 引用，避免明文落盘
	KeyPath  string `json:"key_path,omitempty"`
	// HostKey 是 OpenSSH 的 base64 主机公钥（`ssh-keyscan host` 的输出第二列起，
	// 如 `ssh-ed25519 AAAA...`）。声明了才启用严格校验（见文件头）。
	HostKey string `json:"hostkey,omitempty"`
}

// LoadSSHHosts 读 hosts.json。文件缺失/坏行：缺失 = 空清单（首次运行）；
// 坏 JSON = 报错（宁可让模型看到明确错误，也不静默装作没有主机——
// 那会让「别名不存在」与「配置丢了」混为一谈）。
func LoadSSHHosts(path string) ([]SSHHost, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 SSH 主机配置失败: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var hosts []SSHHost
	if err := json.Unmarshal(data, &hosts); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	for i := range hosts {
		hosts[i].Password = expandEnvRef(hosts[i].Password)
	}
	return hosts, nil
}

// envRefRe 匹配 ${VAR} 引用。
var envRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnvRef 展开 `${VAR}` 引用。未定义的变量保持原样（不静默变空——
// 留着原样文本让错误消息能看见「引用了什么」，且它显然不是可用密码）。
func expandEnvRef(s string) string {
	return envRefRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return m
	})
}

// sshHostLookup 是「按别名查主机」的实现约定（server 装配时注入 hosts.json
// 的读取口；nil = 未装配）。为什么注入而不是注册表直接读文件：配置路径归
// server 装配管（与 models.json 同目录的解析规则在 serve.go），工具层不该
// 知道路由细节。
type sshHostLookup func(alias string) (SSHHost, bool)

// SetSSHHostLookup 注入主机别名解析实现。
func (r *Registry) SetSSHHostLookup(fn sshHostLookup) {
	r.sshMu.Lock()
	r.sshLookup = fn
	r.sshMu.Unlock()
}

// SSHHostLookupFunc 是别名解析函数的导出别名（server 装配层构造闭包用；
// 与 sshHostLookup 同一形状）。
type SSHHostLookupFunc = sshHostLookup

func (r *Registry) getSSHHostLookup() sshHostLookup {
	r.sshMu.Lock()
	defer r.sshMu.Unlock()
	return r.sshLookup
}

// sshAlive 是探活缓存的一条记录。
type sshAlive struct {
	ok    bool
	at    time.Time
	until time.Time
}

// sshGuard 持本进程的探活缓存。进程级单例（注册表同生命周期）是刻意的：
// 探活回答「这条连接最近是不是通的」，是**机器事实**不是会话状态——两个
// 会话先后查同一台设备，后者应该吃到前者的探测结果。
//
// now 可注入（单测的时钟）；mu 保护 map（并行 dispatch 下多会话同时执行
// ssh_hosts 是真实场景）。
type sshGuard struct {
	mu     sync.Mutex
	alive  map[string]sshAlive
	now    func() time.Time
}

func newSSHGuard() *sshGuard {
	return &sshGuard{alive: map[string]sshAlive{}, now: time.Now}
}

// ok 报告 host 是否命中探活缓存（60s 内成功连接过）。
func (g *sshGuard) ok(host string) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e, hit := g.alive[host]
	if !hit {
		return false
	}
	if g.now().After(e.until) {
		delete(g.alive, host) // 过期顺手清
		return false
	}
	return e.ok
}

// mark 记一次连接结果（成功才进缓存；失败每次都要重新探——失败不缓存）。
func (g *sshGuard) mark(host string, ok bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !ok {
		delete(g.alive, host)
		return
	}
	now := g.now()
	g.alive[host] = sshAlive{ok: true, at: now, until: now.Add(sshAliveTTL)}
}

// sshTarget 把别名解析成连接目标（user@host:p 形态由参数拼装负责，
// 这里只给「目标标识」用于探活缓存键）。
type sshTarget struct {
	host   string // 连接目标（缓存键）
	userAt string // user@host（user 可空）
	port   int
	h      SSHHost
}

// resolveTarget 把别名解析成连接要素（纯函数——单测覆盖）。
func resolveTarget(hosts []SSHHost, alias string) (sshTarget, bool) {
	for _, h := range hosts {
		if h.Alias != alias {
			continue
		}
		if h.Host == "" {
			return sshTarget{}, false // 有别名没主机名 = 配置半截
		}
		port := h.Port
		if port == 0 {
			port = 22
		}
		ua := h.Host
		if h.User != "" {
			ua = h.User + "@" + h.Host
		}
		return sshTarget{host: h.Host, userAt: ua, port: port, h: h}, true
	}
	return sshTarget{}, false
}

// sshArgs 拼装 ssh/scp 的公共参数（纯函数——单测覆盖）。
//
// 顺序纪律：认证参数在前（-p 端口、-i key、known_hosts 选项）、目标其后——
// scp 的 -P 与 ssh 的 -p 大小写不同，在这里统一按调用方给的 flag 传入。
func sshArgs(h SSHTarget, portFlag string) []string {
	var args []string
	if h.port != 0 && h.port != 22 {
		args = append(args, portFlag, itoaS(h.port))
	}
	if h.h.KeyPath != "" {
		args = append(args, "-i", h.h.KeyPath, "-o", "IdentitiesOnly=yes")
	}
	if h.h.HostKey != "" {
		// 严格校验：零信任 known_hosts 只含这一个指纹。用 cwd 临时文件——
		// 每条命令独立重建，不污染用户的 ~/.ssh/known_hosts。
		if f := writeHostKeyFile(h.h.HostKey); f != "" {
			args = append(args, "-o", "UserKnownHostsFile="+f, "-o", "StrictHostKeyChecking=yes")
		}
	} else {
		// 未声明指纹：accept-new（首次接受并记录，之后不匹配即拒绝）——
		// 比 no 安全（MITM 首连之后会被发现），比 yes 可用（新设备不用人肉点头）。
		args = append(args, "-o", "StrictHostKeyChecking=accept-new")
	}
	// 批处理语义：交互式提示（密码/指纹）在无人值守的 agent 循环里就是挂死，
	// 一律禁止——认证参数不全时立刻失败，而不是等待输入。
	args = append(args, "-o", "BatchMode=yes")
	args = append(args, "-o", "ConnectTimeout=10")
	return args
}

// SSHTarget 是 sshArgs 的入参（导出命名避免与 sshTarget 混淆的别名——实际是
// 同一形状；单测直接用小写的）。
type SSHTarget = sshTarget

// writeHostKeyFile 把声明的 hostkey 写进临时文件（known_hosts 格式：
// `<host> <keytype> <key>`）。写失败返回空串——调用方回落 accept-new，
// 但连接仍可建立（降级可用，不因配置小错而全拒）。
func writeHostKeyFile(hostKey string) string {
	hostKey = strings.TrimSpace(hostKey)
	if hostKey == "" {
		return ""
	}
	// hostkey 可能是纯 key（`ssh-ed25519 AAAA...`）或已带 host 前缀的
	// known_hosts 行——统一规范化成 known_hosts 行（host 用通配符 `*`，
	// 匹配任何名字——校验强度来自密钥指纹本身，host 字段只是格式要求）。
	line := hostKey
	if !isKnownHostsLine(hostKey) {
		line = "* " + hostKey
	}
	f, err := os.CreateTemp("", "lxcode-known-hosts-")
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		os.Remove(f.Name())
		return ""
	}
	return f.Name()
}

// isKnownHostsLine 判 hostkey 是不是已经是 known_hosts 行格式（三列：
// `host keytype key`——首列不是密钥类型、第二列是）。纯 key（`ssh-ed25519 AAAA...`
// 两列且首列是密钥类型）要补通配符 host 前缀。
func isKnownHostsLine(s string) bool {
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return false
	}
	k := fields[len(fields)-2]
	return strings.HasPrefix(k, "ssh-") || strings.HasPrefix(k, "ecdsa-") || strings.HasPrefix(k, "sk-")
}

// sshHostsDef 构造 ssh_hosts 工具。
//
// RiskHigh：远程执行 = 任意命令落到远程机器——与 bash 同级的确认门。
// Mutates 默认 true（远程执行的副作用静态不可知——只读声明由用户在 mutates
// 勾选框自己决定，工具层不猜）。
func sshHostsDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"host": {"type": "string", "description": "主机别名（hosts.json 里配置的 alias，不是裸地址）"},
			"command": {"type": "string", "description": "要在远程主机上执行的命令"},
			"timeout_s": {"type": "integer", "description": "超时秒数（默认 30，上限 300）"},
			"fresh": {"type": "boolean", "description": "true = 跳过探活缓存，强制先做连接测试"}
		},
		"required": ["host", "command"]
	}`)
	return &Def{
		Name: sshHostsToolName,
		Description: "在**已配置的** SSH 主机（按别名）上执行命令。凭据与端口在 hosts.json 里集中管理，" +
			"不用在命令里拼 sshpass/密码/端口；60 秒内连过的主机跳过连接测试。" +
			"主机清单与凭据由用户在配置目录的 hosts.json 里维护（密码建议用 ${ENV_VAR} 引用）。",
		Parameters: schema,
		Risk:       RiskHigh,
		Mutates:    true,
		Confirm: func(ctx context.Context, args json.RawMessage) string {
			var a struct {
				Host    string `json:"host"`
				Command string `json:"command"`
			}
			err := json.Unmarshal(args, &a)
			if err != nil || a.Command == "" || a.Host == "" {
				return "在远程 SSH 主机执行命令（参数不完整，建议拒绝）"
			}
			return "将在远程主机 [" + a.Host + "] 执行命令: " + a.Command
		},
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Host      string `json:"host"`
				Command   string `json:"command"`
				TimeoutS  int    `json:"timeout_s"`
				Fresh     bool   `json:"fresh"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if strings.TrimSpace(a.Host) == "" {
				return "", fmt.Errorf("host 不能为空（hosts.json 里配置的别名）")
			}
			if strings.TrimSpace(a.Command) == "" {
				return "", fmt.Errorf("command 不能为空")
			}
			lookup := r.getSSHHostLookup()
			if lookup == nil {
				return "", fmt.Errorf("SSH 主机配置未装配（hosts.json 未配置）")
			}
			h, ok := lookup(strings.TrimSpace(a.Host))
			if !ok {
				return "", fmt.Errorf("别名 %q 不在 SSH 主机配置里（hosts.json）——用已配置的别名，或让用户先配置", a.Host)
			}
			if h.Password != "" {
				if _, err := exec.LookPath("sshpass"); err != nil {
					return "", fmt.Errorf("主机 %s 配置了密码认证，但本机没有 sshpass——OpenSSH 不支持从命令行传密码。请改用 key_path（ssh-keygen 生成密钥对并把公钥放到目标主机的 authorized_keys）", a.Host)
				}
			}
			return runSSHCommand(ctx, h, a.Command, a.TimeoutS, a.Fresh, r.sshGuard)
		},
	}
}

// runSSHCommand 执行一次远程命令（探活缓存 → 拼参数 → 前台跑）。
// g 可为 nil（单测直接调纯函数时不走缓存路径——本函数的缓存读写全 nil 安全）。
func runSSHCommand(ctx context.Context, h SSHHost, command string, timeoutS int, fresh bool, g *sshGuard) (string, error) {
	tgt, ok := resolveTarget([]SSHHost{h}, h.Alias)
	if !ok {
		return "", fmt.Errorf("主机配置不完整（缺 host 字段）")
	}
	// 探活缓存：60s 内成功连接过就跳过连接测试（fresh=true 强制重探）。
	// 探活本身 = 带超时的 `ssh ... exit`——失败不缓存，下次还探。
	if fresh || !g.ok(tgt.host) {
		if err := sshProbe(ctx, tgt, h); err != nil {
			return "", fmt.Errorf("连接测试失败: %v", err)
		}
		g.mark(tgt.host, true)
	}
	// 超时钳制：默认 30s，上限 300s（与 bash 同一条纪律）。
	timeout := sshDefaultTo
	if timeoutS > 0 {
		timeout = time.Duration(min(timeoutS, int(sshMaxTo/time.Second))) * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := []string{"ssh"}
	argv = append(argv, sshArgs(tgt, "-p")...)
	argv = append(argv, tgt.userAt, "--", command)
	if h.Password != "" {
		// sshpass 包装：sshpass -e ssh ...（密码经环境变量传递——不出现在
		// 进程命令行里，ps 看不到）。
		argv = append([]string{"-e"}, argv...)
	}
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	if attr := noWindowAttr(); attr != nil {
		cmd.SysProcAttr = attr
	}
	configureProcTree(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd.Process) }
	cmd.WaitDelay = procKillGrace
	if h.Password != "" {
		cmd.Env = append(os.Environ(), "SSHPASS="+h.Password)
	}
	out, err := cmd.CombinedOutput()
	result := normalizeConsoleOutput(out)
	if len(result) > bashMaxOutput {
		result = truncateUTF8(result, bashMaxOutput) + "\n…（输出超 32KB 已截断）"
	}
	note := execNote(cctx, err, timeout)
	// 连接失败（255 = ssh 的通用错误码）时探活缓存作废——下次重新探。
	if code := exitCodeOf(err); code == 255 {
		g.mark(tgt.host, false)
	}
	if result == "" && note == "" {
		return "（无输出，退出码 0）", nil
	}
	return result + note, nil
}

// sshProbe 探活：带超时的 `ssh <args> exit`（退出码 0 = 通）。
func sshProbe(ctx context.Context, tgt sshTarget, h SSHHost) error {
	cctx, cancel := context.WithTimeout(ctx, sshConnectProbe)
	defer cancel()
	argv := []string{"ssh"}
	argv = append(argv, sshArgs(tgt, "-p")...)
	argv = append(argv, tgt.userAt, "exit")
	if h.Password != "" {
		argv = append([]string{"-e"}, argv...)
	}
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	if attr := noWindowAttr(); attr != nil {
		cmd.SysProcAttr = attr
	}
	if h.Password != "" {
		cmd.Env = append(os.Environ(), "SSHPASS="+h.Password)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(normalizeConsoleOutput(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s（%v）", msg, err)
	}
	return nil
}

// exitCodeOf 提取退出码（非 ExitError 返回 -1）。
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if asExitErr(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// asExitErr 包装 errors.As（本文件不直接引 errors，减少 import 面）。
func asExitErr(err error, target **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// itoaS 是 int 转字符串（本文件局部用）。
func itoaS(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
