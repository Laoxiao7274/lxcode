package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// 自定义工具的执行面（M4）：目录里 source=binary 的条目在运行时注册进
// 注册表——工具作者声明的命令模板（{param} 占位）经进程边界执行。
//
// 与 bash 的关键差别（有意为之）：**不经过 shell**。模板按空白切分、参数值
// 原样作为单个 argv 传入，因此引号、分号、$ 全是普通字符——自定义工具是
// 用户预定义的窄接口，不接受任意命令文本，注入面在结构上消失。
//
// 参数只支持标量（string/number/bool）：工具作者声明的是参数名与类型，
// 数组/对象没有合理的单参展开语义（需要时拆成多个参数）。

const (
	// customTimeout 对齐 bash 的默认超时：自定义工具是窄接口，不给
	// timeout 参数（参数面由工具作者声明，塞保留名会与其参数撞车）。
	customTimeout = bashDefaultTimeout
	// customMaxOutput 对齐 bash 的输出上限。
	customMaxOutput = bashMaxOutput
)

// CustomDef 把目录条目转成可执行的工具定义。
//
// 只接受 source=binary：builtin 的实现在注册表里（目录条目只是元数据），
// mcp 的执行面由 MCP 客户端在注册时提供。返回错误 = 该条目不可执行
// （调用方跳过并记录，不该让一条坏目录把整份注册表带下水）。
func CustomDef(spec sessiondata.ToolSpec) (*Def, error) {
	if spec.Source != "binary" {
		return nil, fmt.Errorf("source=%s 不是 binary", spec.Source)
	}
	template := strings.TrimSpace(spec.Command)
	if template == "" {
		return nil, errors.New("未配置 command（binary 工具需要 {param} 占位模板）")
	}
	declared, err := paramSchema(spec.Params)
	if err != nil {
		return nil, fmt.Errorf("参数定义非法: %w", err)
	}
	// 模板里的占位符必须都是已声明的参数：拼错的名字在这里就报出来，
	// 而不是等模型调用时才发现（那时错误会落在模型头上）
	if err := checkPlaceholders(template, spec.Params); err != nil {
		return nil, err
	}

	desc := strings.TrimSpace(spec.Desc)
	if ex := strings.TrimSpace(spec.Example); ex != "" {
		desc += "\n示例: " + ex
	}
	risk := RiskLow
	if spec.Risk == "high" {
		risk = RiskHigh
	}
	render := func(args json.RawMessage) ([]string, error) {
		return renderCommand(spec, template, args)
	}
	return &Def{
		Name:        spec.ID,
		Description: desc,
		Parameters:  declared,
		Risk:        risk,
		// 起进程即变更外部世界：strict 只读模式必须拒绝（与 bash 同款语义）
		Mutates: true,
		Confirm: func(_ context.Context, args json.RawMessage) string {
			if risk != RiskHigh {
				return ""
			}
			argv, err := render(args)
			if err != nil {
				return "执行自定义工具 " + spec.ID + "（参数不完整，建议拒绝）"
			}
			// 确认卡给出渲染后的完整命令：用户批准前必须看到实际要跑什么
			return "将执行: " + strings.Join(argv, " ")
		},
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			argv, err := render(args)
			if err != nil {
				return "", err
			}
			return runArgv(ctx, argv)
		},
	}, nil
}

// paramSchema 把目录里的参数描述转成 JSON Schema（透传给模型的工具声明）。
func paramSchema(params []sessiondata.ToolParam) (json.RawMessage, error) {
	props := make(map[string]map[string]any, len(params))
	var required []string
	for _, p := range params {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return nil, errors.New("参数名不能为空")
		}
		if _, dup := props[name]; dup {
			return nil, fmt.Errorf("参数名重复: %s", name)
		}
		prop := map[string]any{"type": jsonType(p.Type)}
		if p.Desc != "" {
			prop["description"] = p.Desc
		}
		props[name] = prop
		if p.Required {
			required = append(required, name)
		}
	}
	root := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		root["required"] = required
	}
	b, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("序列化参数 schema 失败: %w", err)
	}
	return b, nil
}

// jsonType 把目录里的参数类型映射到 JSON Schema 类型。目录是给人和模型看的
// 简写（int/regex/enum），schema 必须是合法 JSON Schema 类型。
func jsonType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "int", "integer":
		return "integer"
	case "number", "float":
		return "number"
	case "bool", "boolean":
		return "boolean"
	case "array", "list":
		return "array"
	case "object", "map":
		return "object"
	default:
		// string / regex / enum / path / 空 —— 一律按字符串
		return "string"
	}
}

// checkPlaceholders 校验模板里的 {name} 都是已声明的参数。
func checkPlaceholders(template string, params []sessiondata.ToolParam) error {
	known := make(map[string]bool, len(params))
	for _, p := range params {
		known[strings.TrimSpace(p.Name)] = true
	}
	for _, ph := range placeholders(template) {
		if !known[ph] {
			return fmt.Errorf("command 模板里的 {%s} 不是已声明的参数", ph)
		}
	}
	return nil
}

// placeholders 提取模板里出现的占位符名（去重，稳定顺序）。
func placeholders(template string) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(template); i++ {
		if template[i] != '{' {
			continue
		}
		j := strings.IndexByte(template[i:], '}')
		if j < 0 {
			break
		}
		name := template[i+1 : i+j]
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		i += j
	}
	return out
}

// renderCommand 把参数填进命令模板，返回 argv。
//
// 渲染规则（无 shell 的核心保障）：模板按空白切分成 token；token 内出现
// {name} 就替换成参数值——整个 token 就是一个占位符时，值原样成为单个 argv
// （值里的空格不会被再切分，这就是"值原样单参"）；否则做前缀/后缀拼接
// （如 --path={p}）。
//
// 可选参数（Required=false）缺省时**丢掉整个 token**——`rg {pattern} {path}`
// 这类模板里 path 不给就退化成 `rg {pattern}`（默认搜当前目录），而不是报错；
// 必填参数缺省才报错（错误信息列出可用参数，模型可自行纠正）。
func renderCommand(spec sessiondata.ToolSpec, template string, args json.RawMessage) ([]string, error) {
	vals, err := scalarArgs(args)
	if err != nil {
		return nil, err
	}
	required := make(map[string]bool, len(spec.Params))
	known := make(map[string]bool, len(spec.Params))
	for _, p := range spec.Params {
		name := strings.TrimSpace(p.Name)
		known[name] = true
		if p.Required {
			required[name] = true
		}
	}
	// 未知参数直接报错：静默忽略会让"模型传错参数名却拿到正常结果"，
	// 更难查（错误信息列出可用参数，模型可自行纠正）
	for _, name := range sortedKeys(vals) {
		if !known[name] {
			return nil, fmt.Errorf("工具 %s 不支持参数 %q（可用: %s）", spec.ID, name, strings.Join(paramNames(spec), ", "))
		}
	}
	tokens := strings.Fields(template)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("工具 %s 的 command 模板为空", spec.ID)
	}
	argv := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		out, missing, err := substitute(tok, vals, required)
		if err != nil {
			return nil, fmt.Errorf("工具 %s: %w", spec.ID, err)
		}
		if missing != "" {
			// 可选参数缺省：整个 token 丢掉（如 --glob={glob} / 单独的 {path}）
			continue
		}
		argv = append(argv, out)
	}
	if len(argv) == 0 || argv[0] == "" {
		return nil, fmt.Errorf("工具 %s 的可执行文件名渲染为空（command 模板缺了必填参数？）", spec.ID)
	}
	return argv, nil
}

// substitute 把单个 token 里的 {name} 全部替换为参数值。
// 返回 missing 非空 = 该 token 引用了缺省的可选参数，调用方丢掉整个 token；
// 缺的是必填参数则直接报错（模型必须给出）。
func substitute(tok string, vals map[string]string, required map[string]bool) (out, missing string, err error) {
	var b strings.Builder
	for i := 0; i < len(tok); {
		if tok[i] != '{' {
			b.WriteByte(tok[i])
			i++
			continue
		}
		j := strings.IndexByte(tok[i:], '}')
		if j < 0 {
			// 没有配对的 '}'：当普通字符（模板已校验过占位符，这里只做容错）
			b.WriteString(tok[i:])
			break
		}
		name := tok[i+1 : i+j]
		v, ok := vals[name]
		if !ok {
			if required[name] {
				return "", "", fmt.Errorf("缺少参数 {%s}（必填参数必须由模型给出）", name)
			}
			return "", name, nil
		}
		b.WriteString(v)
		i += j + 1
	}
	return b.String(), "", nil
}

// scalarArgs 解析工具调用参数为标量文本表。非标量（数组/对象）与 null 直接
// 报错：没有合理的单参展开语义，猜一个比报错更糟。
func scalarArgs(args json.RawMessage) (map[string]string, error) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return map[string]string{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("参数解析失败: %w", err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch t := v.(type) {
		case string:
			out[k] = t
		case json.Number:
			out[k] = t.String()
		case bool:
			out[k] = strconv.FormatBool(t)
		case nil:
			return nil, fmt.Errorf("参数 %q 是 null（要么给值，要么不传）", k)
		default:
			return nil, fmt.Errorf("参数 %q 不是标量（只支持字符串/数字/布尔；数组与对象请拆成多个参数）", k)
		}
	}
	return out, nil
}

// paramNames 返回已声明参数名（错误信息用，稳定顺序）。
func paramNames(spec sessiondata.ToolSpec) []string {
	out := make([]string, 0, len(spec.Params))
	for _, p := range spec.Params {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

// runArgv 执行 argv（无 shell），返回给模型的结果文本。
// cwd = 会话工作目录（项目会话 = 项目根），与 bash/read_file 的相对路径基准一致。
func runArgv(ctx context.Context, argv []string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, customTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	if wd := WorkDir(ctx); wd != "" {
		cmd.Dir = wd
	}
	out, err := cmd.CombinedOutput()
	result := string(out)
	if len(result) > customMaxOutput {
		result = result[:customMaxOutput] + "\n…（输出超 32KB 已截断）"
	}
	note := execNote(cctx, err, customTimeout)
	if result == "" && note == "" {
		return "（无输出，退出码 0）", nil
	}
	return result + note, nil
}
