// project_create 工具（助手项目全景三件套之三）：在指定父目录下新建项目文件夹
// 并注册进项目列表——「在 xxx 下建一个 yyy 项目」的直接执行面。
//
// 与 project_list 同一条注入模式：tools 是叶子包不 import project/store，
// 注册表只持「创建并注册」这一个函数，server 装配时接线。未装配 =
// 该能力不存在（报人话错误，不编结果）。
//
// 安全边界（auto 档下这是助手唯一能写磁盘新建目录的面）：
//   - ConfirmForced 硬闸——即使 auto（完全无确认）也弹确认门（agent 工具循环
//     消费它）；confirm 档走常规 Confirm，文案同一份；
//   - name 是文件夹名不是路径：禁路径分隔符 / .. / 空白裸串，杜绝 ../ 逃逸；
//   - parent 必须是**已存在的绝对路径目录**（project.ValidateDirectory）；
//   - 目标 <parent>/<name> 必须不存在或为**空目录**（非空拒绝——不往一个
//     有内容的目录里 git init + 注册）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ProjectCreateToolName 是 project_create 工具的 id。
//
// 必须匹配 `^[a-zA-Z0-9_-]{1,64}$`（与 ProjectListToolName 同一条工具名约束
// ——越界的名字严格端点 400 拒收整轮）。
const ProjectCreateToolName = "project_create"

// projectCreateRefFn 是 projectCreateDef 注册时绑定的注册表引用（New() 单线程
// 注册，运行期只读——与 projectListRefFn 同一「装配期写一次」语义）。
var projectCreateRefFn func() *Registry

// ProjectCreateFn 是「新建项目文件夹并注册」的实现约定：server 装配时接线
//（mkdir → git init → AddProject → 广播 project.changed——这些是 project/store
// 层的知识，工具层不碰文件系统）。name 已过本文件的安全校验。
type ProjectCreateFn func(ctx context.Context, name, parent string) (ProjectCreateResult, error)

// ProjectCreateResult 是接线实现回给工具渲染的行数据。
type ProjectCreateResult struct {
	ID   string
	Name string
	Path string
}

// SetProjectCreate 注入项目创建实现（server 装配时调用）。
func (r *Registry) SetProjectCreate(fn ProjectCreateFn) {
	r.searchMu.Lock()
	r.projectCreate = fn
	r.searchMu.Unlock()
}

func (r *Registry) getProjectCreate() ProjectCreateFn {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.projectCreate
}

// validateProjectName 是项目名（= 文件夹名）的安全校验：禁路径分隔符、..、
// 空白裸串——参照 project_list.go 参数校验的同一风格。通过返回 nil。
func validateProjectName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("项目名（文件夹名）不能为空")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("项目名只能是单层文件夹名，不能包含路径分隔符或 ..（收到: %q）", name)
	}
	return nil
}

// projectCreateConfirm 组装确认文案（Confirm 与 ConfirmForced 共用一份）。
func projectCreateConfirm(_ context.Context, args json.RawMessage) string {
	var p struct {
		Name   string `json:"name"`
		Parent string `json:"parent"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Name == "" {
		return "助手要新建项目文件夹，参数不完整——确认放行吗？"
	}
	return fmt.Sprintf("助手要在 %s 下新建项目文件夹「%s」（mkdir + git init + 注册进项目列表）。确认执行吗？", p.Parent, p.Name)
}

// projectCreateDef 返回 project_create 的注册声明。
//
// 风险定级：高危 + 变更（mkdir + git init + 写项目注册表）。注册进内置清单但
// 不默认给主/子 Agent——项目创建是助手（跨项目视角）的能力面；需要时用户可在
// Agent 编辑器显式勾选（目录种子有它，chip 可勾）。
func projectCreateDef(r *Registry) *Def {
	projectCreateRefFn = func() *Registry { return r }
	return &Def{
		Name: ProjectCreateToolName,
		Description: "在指定父目录下新建项目文件夹（mkdir + git init + 注册进项目列表）。" +
			"用户说「在 xxx 下建一个 yyy 项目」时用它；name 是文件夹名（不能带路径分隔符），" +
			"parent 是父目录的绝对路径。执行前需用户确认。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"项目名=文件夹名（单层，禁路径分隔符与 ..）"},"parent":{"type":"string","description":"父目录的绝对路径（必须已存在）"}},"required":["name","parent"]}`),
		Risk:    RiskHigh,
		Mutates: true,
		Confirm: projectCreateConfirm,
		// ConfirmForced：auto 档（完全无确认）的唯一例外口——agent 工具循环
		// 消费它（runTools 的确认门分支特判）。理由见文件头安全边界。
		ConfirmForced: projectCreateConfirm,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Name   string `json:"name"`
				Parent string `json:"parent"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if err := validateProjectName(p.Name); err != nil {
				return "", err
			}
			if strings.TrimSpace(p.Parent) == "" {
				return "", fmt.Errorf("parent（父目录绝对路径）必填")
			}
			if projectCreateRefFn == nil {
				return "", fmt.Errorf("项目创建未装配（后端未挂载会话存储），无法新建项目")
			}
			fn := projectCreateRefFn().getProjectCreate()
			if fn == nil {
				return "", fmt.Errorf("项目创建未装配（后端未挂载会话存储），无法新建项目")
			}
			res, err := fn(ctx, p.Name, p.Parent)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("已创建并注册项目「%s」：%s（project_id: %s）。"+
				"可立即用 run_task（project_id 填上面这个）往它派任务。",
				res.Name, res.Path, res.ID), nil
		},
	}
}
