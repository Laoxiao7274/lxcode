// project_list 工具（助手项目全景三件套之二）：列出用户的全部项目——
// id/name/path + 顶层会话数 + 最近活动。
//
// 为什么必须是工具而不只是提示词注入：注入段有条数上限（>20 个项目只给头部
// + 尾注），且注入是**每轮快照**（本轮组装时读一次）——长会话里用户中途注册
// 新项目，注入段就过时了。工具是**实时查询**，两者共用 ListProjectStats
// 同一份数据源，口径永远一致（工具=注入的超集：没有条数上限）。
//
// 注入模式（与 SetSessionSearch / SetWebSearch 同款）：tools 是叶子包不 import
// store，注册表只持「查项目统计」这一个函数，server 装配时接线。未装配 =
// 该能力不存在（报人话错误，不编项目）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// ProjectListToolName 是 project_list 工具的 id。
//
// 必须匹配 `^[a-zA-Z0-9_-]{1,64}$`（与 RunTaskToolName 同一条工具名约束——
// 越界的名字严格端点 400 拒收整轮）。
const ProjectListToolName = "project_list"

// projectListRefFn 是 projectListDef 注册时绑定的注册表引用（New() 单线程
// 注册，运行期只读——与 execer 注入同一「装配期写一次」语义）。
var projectListRefFn func() *Registry

// ProjectListFn 是项目全景查询的实现约定：server 装配时接线（store 统计 +
// 运行中会话数的合流——busy 是 server 层事实，store 不知道哪些会话在跑）。
// 返回已格式化时间与统计口径的完整行数据，渲染归本工具。
type ProjectListFn func(ctx context.Context) ([]sessiondata.ProjectStats, error)

// SetProjectList 注入项目全景查询实现（server 装配时调用）。
func (r *Registry) SetProjectList(fn ProjectListFn) {
	r.searchMu.Lock()
	r.projectList = fn
	r.searchMu.Unlock()
}

func (r *Registry) getProjectList() ProjectListFn {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.projectList
}

// projectListDef 返回 project_list 的注册声明。
//
// 风险定级：低危、只读（Mutates 缺省 false）——一条 SELECT，不碰任何文件。
// 注册进内置清单但**不默认给主/子 Agent**：主域 Agent 活在自己项目里，项目
// 全景是助手（跨项目视角）的能力面；需要时用户可在 Agent 编辑器显式勾选
//（目录种子有它，chip 可勾）。
func projectListDef(r *Registry) *Def {
	projectListRefFn = func() *Registry { return r }
	return &Def{
		Name: ProjectListToolName,
		Description: "列出用户的全部项目（id、名称、路径、会话数、最近活动、运行中的会话数）。" +
			"用户问「我有哪些项目」或提到某个项目时先用它；" +
			"run_task 的 project_id 从这里取（id 是不可读的时间戳随机串，只能从这里拿到）。" +
			"返回按最近活动倒序。",
		Parameters: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
		Risk:       RiskLow,
		Exec: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if projectListRefFn == nil {
				return "", fmt.Errorf("项目列表未装配（后端未挂载会话存储），无法列出项目")
			}
			fn := projectListRefFn().getProjectList()
			if fn == nil {
				return "", fmt.Errorf("项目列表未装配（后端未挂载会话存储），无法列出项目")
			}
			stats, err := fn(ctx)
			if err != nil {
				return "", err
			}
			if len(stats) == 0 {
				return "用户还没有注册任何项目。项目经客户端「项目」入口注册（指定一个目录）；注册后才能对它派发任务。", nil
			}
			out := fmt.Sprintf("共 %d 个项目（按最近活动倒序）：\n", len(stats))
			for i := range stats {
				ps := stats[i]
				activity := "无活动"
				if ps.UpdatedAt != "" {
					activity = "最近活动 " + ps.UpdatedAt
				}
				running := ""
				switch ps.Running {
				case 0:
				case 1:
					running = "，运行中"
				default:
					running = fmt.Sprintf("，运行中 %d", ps.Running)
				}
				out += fmt.Sprintf("- %s（%s）：%s，%d 个会话，%s%s\n", ps.ID, ps.Name, ps.Path, ps.Sessions, activity, running)
			}
			return out, nil
		},
	}
}
