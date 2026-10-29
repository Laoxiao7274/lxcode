package protocol

import (
	"github.com/moyunteng/lxcode/internal/config"
)

// ModelListResult 是注册表快照（模型 + 角色绑定），也是 model.changed 事件载荷。
type ModelListResult struct {
	Models []config.ModelConfig `json:"models"`
	Roles  map[string]string    `json:"roles"`
}

type ModelRemoveParams struct {
	ID string `json:"id"`
}

type ModelEnableParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type RoleSetParams struct {
	Role    string `json:"role"`
	ModelID string `json:"model_id"` // 空串 = 解绑
}

// ModelCatalogListParams 是 model.catalog.list 的参数。
// Refresh 忽略 TTL 强制重拉（用户点「刷新目录」时用）。
type ModelCatalogListParams struct {
	Refresh bool `json:"refresh,omitempty"`
}

// ModelCatalogModelsParams 是 model.catalog.models 的参数（按厂商取模型明细）。
type ModelCatalogModelsParams struct {
	Provider string `json:"provider"`
	Refresh  bool   `json:"refresh,omitempty"`
}

// ModelDiscoverParams 是 model.discover 的参数（探测端点的模型清单）。
//
// 两种寻址方式，ID 优先：
//   - ID：已注册模型条目的 id——复用它那条的连接配置（key 不必再过一遍 wire）；
//   - BaseURL + APIKey + Format：还没进注册表的新端点（自定义提供商表单）。
type ModelDiscoverParams struct {
	ID      string `json:"id,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	Format  string `json:"format,omitempty"`
}

// ---- Agent 注册表与拓展目录（M1）----
// 协议载荷是 store/sessiondata 类型的 wire 形态（snake_case JSON tag；
// 映射在 server 层——store 类型过协议边界必须经映射，历史 bug 两次踩过）。

// AgentEntry 是 agent.list 的条目（AgentDef 的 wire 形态）。
type AgentEntry struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Desc      string   `json:"desc"`
	Color     string   `json:"color"`
	Model     string   `json:"model"`
	Tools     []string `json:"tools"`
	Workflow  string   `json:"workflow"`
	Skills    []string `json:"skills"`
	Delegates []string `json:"delegates"`
	Approval  string   `json:"approval"`
	Enabled   bool     `json:"enabled"`
	IsMain    bool     `json:"is_main,omitempty"`
	Prompt    string   `json:"prompt"`
	Protocol  string   `json:"protocol"`
	Custom    bool     `json:"custom"`
}

// AgentAddParams 是 agent.add 的载荷（也是 agent.update——全量替换语义）。
type AgentAddParams struct {
	Agent AgentEntry `json:"agent"`
}

// AgentRemoveParams 是 agent.remove 的参数。
type AgentRemoveParams struct {
	ID string `json:"id"`
}

// ModuleEntry 是 catalog.modules.list 的条目。
type ModuleEntry struct {
	ID     string `json:"id"`
	Desc   string `json:"desc"`
	Kind   string `json:"kind"` // process（模板） | skill（技能）
	Body   string `json:"body"`
	Custom bool   `json:"custom"`
}

// ModuleAddParams 是 catalog.modules.add/update 的载荷。
type ModuleAddParams struct {
	Module ModuleEntry `json:"module"`
}

// ModuleRemoveParams 是 catalog.modules.remove 的参数。
type ModuleRemoveParams struct {
	ID string `json:"id"`
}

// ToolParamEntry 是工具参数的 wire 形态。
type ToolParamEntry struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Desc     string `json:"desc,omitempty"`
}

// ToolEntry 是 catalog.tools.list 的条目。
type ToolEntry struct {
	ID          string           `json:"id"`
	Desc        string           `json:"desc"`
	Risk        string           `json:"risk"`   // low | high
	Source      string           `json:"source"` // builtin | binary | mcp
	Params      []ToolParamEntry `json:"params,omitempty"`
	Doc         string           `json:"doc,omitempty"`
	Server      string           `json:"server,omitempty"`  // source=mcp 的来源服务器
	Command     string           `json:"command,omitempty"` // binary：{param} 占位模板
	Example     string           `json:"example,omitempty"`
	PackageFile string           `json:"package_file,omitempty"`
	Custom      bool             `json:"custom"`
}

// ToolAddParams 是 catalog.tools.add/update 的载荷。
type ToolAddParams struct {
	Tool ToolEntry `json:"tool"`
}

// ToolRemoveParams 是 catalog.tools.remove 的参数。
type ToolRemoveParams struct {
	ID string `json:"id"`
}

// McServerEntry 是 catalog.mcp.list 的条目。
//
// Status/ToolCount/LastError/Stderr 是**运行期**信息（由服务端在应答时合成，
// 不是磁盘形状）：界面据此显示「已连接 / 连接失败 + 原因 / 已停止」。
// 新增字段是加法变更，不递增协议 Version。
type McServerEntry struct {
	ID        string            `json:"id"`
	Desc      string            `json:"desc"`
	Transport string            `json:"transport"` // stdio | sse
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Enabled   bool              `json:"enabled"`
	Custom    bool              `json:"custom"`

	// Status：connected | error | stopped（停用或未对账到）。
	Status string `json:"status,omitempty"`
	// ToolCount 是已列举到的工具数（未连接时为 0）。
	ToolCount int `json:"tool_count,omitempty"`
	// LastError 是最近一次连接/列举失败的原因（成功时为空）。
	LastError string `json:"last_error,omitempty"`
	// Stderr 是 stdio 子进程 stderr 的尾部（诊断用，可能为空）。
	Stderr string `json:"stderr,omitempty"`
}

// McServerAddParams 是 catalog.mcp.add/update 的载荷。
type McServerAddParams struct {
	Server McServerEntry `json:"server"`
}

// McServerRemoveParams 是 catalog.mcp.remove 的参数。
type McServerRemoveParams struct {
	ID string `json:"id"`
}

// AgentChangedParams 是 agent.changed 事件的载荷（客户端重拉 agent.list）。
type AgentChangedParams struct {
	Reason string `json:"reason"` // add | update | remove
}

// CatalogChangedParams 是 catalog.changed 事件的载荷：kind 标明哪个目录
// 变了（modules/tools/mcp），客户端只重拉对应列表。
type CatalogChangedParams struct {
	Kind   string `json:"kind"`   // modules | tools | mcp
	Reason string `json:"reason"` // add | update | remove
}
