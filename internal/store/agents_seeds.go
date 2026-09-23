// 种子数据（首次建库注入；内容与前端 agent-seeds.ts 的目录部分对齐——
// 演示 Agent 名单后端给主 + 代码 + 调研 + 测试四个（覆盖「实现 / 勘察 / 验证」
// 三种执行面），其余（审查、运维）是前端演示专属，真实后端不替用户预置）。
// 新增种子 Agent 的落地纪律见 agents.go 的 syncCatalogSeeds：缺失才插入，
// 已有行一律不碰——老库因此也能吃到，而用户的编辑不会被覆盖。
package store

import "github.com/moyunteng/lxcode/internal/sessiondata"

var seedTools = []sessiondata.ToolSpec{
	{
		ID: "read_file", Desc: "按行读取文件（分页、256KB 上限）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "path", Type: "string", Required: true, Desc: "文件路径（相对会话工作目录）"}, {Name: "offset", Type: "int", Desc: "起始行号（1 起）"}, {Name: "limit", Type: "int", Desc: "行数上限"}},
		Doc:    "按行输出（`行号→` 前缀）。\n\n- 256KB 上限，超出建议用 search 定位再分段读\n- 二进制文件拒绝（避免乱码进上下文）",
	},
	{
		ID: "search", Desc: "按正则检索文件内容与文件名", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "pattern", Type: "regex", Required: true}, {Name: "path", Type: "string", Desc: "检索根目录"}, {Name: "mode", Type: "enum", Desc: "files / content / count"}},
		Doc:    "纯 Go RE2 检索，不经过 shell。\n\n- files 模式列文件名；content 列命中行；count 只给计数\n- 大仓库先 files 缩小范围再 content",
	},
	{
		ID: "edit", Desc: "精确替换文件内容（old_string 唯一匹配）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "path", Type: "string", Required: true}, {Name: "old_string", Type: "string", Required: true}, {Name: "new_string", Type: "string", Required: true}},
		Doc:    "精确替换——old_string 必须在文件中唯一匹配（0 或 >1 都报错）。\n\n原子写；这是编程任务的主编辑通道。",
	},
	{
		ID: "write_file", Desc: "全量写文件（新建或覆盖）", Risk: "high", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "path", Type: "string", Required: true}, {Name: "content", Type: "string", Required: true}},
		Doc:    "全量覆盖：覆盖已有文件需确认；缩水守卫（覆盖后 <50% 会告警）。",
	},
	{
		ID: "bash", Desc: "执行 shell 命令（超时兜底）", Risk: "high", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "command", Type: "string", Required: true}, {Name: "timeout_ms", Type: "int", Desc: "毫秒（默认 60000）"}, {Name: "stdin", Type: "string", Desc: "标准输入（≤64KB）"}},
		Doc:    "超时 60s/上限 300s、输出 32KB、stdin ≤64KB。\n\nWindows 下自动选 Git Bash；高危走确认门。",
	},
	{
		ID: "todo", Desc: "任务清单全量写入", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "items", Type: "array", Required: true, Desc: "[{content, status: pending|active|done}]"}},
		Doc:    "多步任务的过程对齐——每完成一步更新状态，清单是唯一事实源；active 项唯一。",
	},
	{
		ID: "session_search", Desc: "搜历史会话内容", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "pattern", Type: "regex", Required: true}},
		Doc:    "跨全部会话的消息内容检索（含当前）。",
	},
	{
		// 内置的渐进披露读取口：目录里没有它，子 Agent 就「拿到技能索引却没法取正文」
		// （技能多选注入因此形同虚设）——用户拍板顺手补上。
		ID: "read_skill", Desc: "读取技能模块的完整内容（提示词只列索引）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "id", Type: "string", Required: true, Desc: "技能 id（提示词「可用技能」清单里的名字）"}},
		Doc:    "渐进披露：提示词只注入技能索引（id + 摘要），需要完整方法论时按 id 取全文。\n\n没在白名单里的技能读不到（提示词里看不到 = 不存在）。",
	},
	{
		// 外部 Rust 二进制的接入样板（AGENTS.md §2.1「Go 主刀、Rust 武器库」）：
		// command 必须是**可运行**的模板——空 command 的工具进不了注册表，
		// 模型会如实回答「注册表没有」（用户报告过的原始现象）。
		// rg 未安装时调用会回填「[启动失败: ...]」，自解释。
		ID: "ripgrep", Desc: "Rust 检索二进制——大仓库全文搜索", Risk: "low", Source: "binary", Custom: false,
		Command: "rg -n --no-heading --color=never --glob={glob} {pattern} {path}",
		Params: []sessiondata.ToolParam{
			{Name: "pattern", Type: "regex", Required: true, Desc: "检索正则（RE2 语法）"},
			{Name: "path", Type: "string", Desc: "检索根目录（默认会话工作目录）"},
			{Name: "glob", Type: "string", Desc: "文件名过滤（如 *.go）"},
		},
		Doc: "Rust 检索二进制，经进程边界接入（Go 主刀、Rust 武器库）。\n\n大仓库全文搜索比内置 search 快一个量级；参数与 rg CLI 对齐（path/glob 可选——不给就搜会话工作目录）。\n\n未安装 rg 时调用会报「启动失败」：装法 `winget install BurntSushi.ripgrep.MSVC`（或 `cargo install ripgrep`）。",
	},
	{
		// 声明了但没实现（浏览器面板在路线图上）：command 留空 = 目录里可见但
		// **不进注册表**，模型会被告知「当前不可用」；等接入真实驱动再填 command。
		ID: "browser", Desc: "Chromium 面板驱动（页面勘察与截图）", Risk: "low", Source: "binary", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "url", Type: "string", Required: true}, {Name: "action", Type: "enum", Desc: "navigate / snapshot / click"}},
		Doc:    "Chromium 面板驱动。\n\n三段式：navigate 导航 → snapshot 快照定位 → click 操作。\n\n**未配置**：还没有对应的驱动二进制——先在拓展页把 command 填上（或删掉这条）才会进注册表。",
	},
}

var seedModules = []sessiondata.ModuleSpec{
	{
		ID: "plan-execute-verify", Kind: "process", Custom: false,
		Desc: "规划 → 执行 → 验证：先出方案再动手，完成后验证再交付",
		Body: "# 规划 → 执行 → 验证\n\n任何非平凡任务先出**方案**再动手，完成后**验证**再交付。\n\n## 规划\n- 理解目标与边界（输入、期望产物、约束）\n- 列出改动面与风险点\n\n## 执行\n- 按方案推进；偏离即停下重新评估\n- 长任务维护 todo\n\n## 验证\n- 构建与测试通过才算完成\n- 结果如实报告：做了什么、输出是什么、有什么问题",
	},
	{
		ID: "minimal-change", Kind: "process", Custom: false,
		Desc: "最小改动——只动达成目标所必需的部分",
		Body: "# 最小改动\n\n只动达成目标所必需的部分。\n\n- 不顺手重构、不顺手清理\n- 发现范围外的问题：记录，不顺手修\n- 改动范围与目标偏差时先停下重新对齐",
	},
	{
		ID: "research-first", Kind: "process", Custom: false,
		Desc: "先查证再断言——不确定的就先检索",
		Body: "# 先查证再断言\n\n不确定的就先检索，检索不到就明说。\n\n- 结论必须给依据（代码行号 / 文档链接 / 命令输出）\n- 查不到 ≠ 不存在——如实报告「未找到」而不是编造\n- 引用别人的结论先验证",
	},
	{
		ID: "frontend-design", Kind: "skill", Custom: false,
		Desc: "前端视觉设计——排版、留白与层级",
		Body: "# 前端视觉设计\n\n- 排版优先：字号/行高/字重构成层级，不靠颜色堆\n- 留白是设计的一部分——拥挤是偷懒\n- 少即是多：每个装饰都要能说出为什么存在",
	},
	{
		ID: "gsap", Kind: "skill", Custom: false,
		Desc: "GSAP 动效——时间线与缓动",
		Body: "# GSAP\n\n- 时间线（timeline）组织序列；标签定位关键帧\n- 缓动 ease 是性格：power2.out 通用，back.out 弹性\n- 入场动画收尾 clearProps——残留 transform 会困住弹层 z-index",
	},
	{
		ID: "windows-app-forensics", Kind: "skill", Custom: false,
		Desc: "Windows 桌面应用排障——进程/更新器/安装残留",
		Body: "# Windows 桌面应用排障\n\n- 安装器卡「应用运行中」：查进程树（父子关系），路径前缀匹配比名字可靠\n- 更新器报错：证据三件套——文件头字节 / 哈希 / 在线 URL 探活\n- 扩展名缺失的缓存文件：读头字节辨类型",
	},
	{
		ID: "shadcn", Kind: "skill", Custom: false,
		Desc: "组件库工程与注册表",
		Body: "# shadcn/ui\n\n组件库工程与注册表。\n\n- 组件按 registry 分发，不整包引入\n- 主题走 CSS 变量，不 fork 组件改样式\n- 升级以 diff 合并，不锁定版本",
	},
}

var seedAgents = []sessiondata.AgentDef{
	{
		ID: "main", Name: "主 Agent", IsMain: true, Enabled: true, Color: "#0d0d0d", Model: "",
		Desc:     "决策与分派中枢：理解意图、拆解任务、调用名单中的 Agent 并验收汇总。不直接执行任务。",
		Tools:    []string{"agent_dispatch"},
		Prompt:   "",
		Workflow: "plan-execute-verify", Skills: []string{},
		Delegates: []string{"coder", "researcher", "tester"}, Approval: "confirm",
	},
	{
		ID: "coder", Name: "代码 Agent", Enabled: true, Color: "#3b82f6", Model: "",
		Desc:     "编码实现与重构：读写代码、跑构建测试，产出可验证的改动。",
		Prompt:   "你是代码 Agent。改动前先读相关代码，遵守仓库规范；每步改动可解释、可回退，构建测试通过才算完成。",
		Tools:    []string{"read_file", "search", "edit", "write_file", "bash", "todo"},
		Workflow: "minimal-change", Skills: []string{"frontend-design", "gsap"},
		Delegates: []string{}, Approval: "confirm",
	},
	{
		// 调研面：只读工具集（无 edit/write_file/bash），所以「不改任何文件」是
		// 结构保证而不是靠提示词自觉；approval=strict 是同一件事的第二道保险
		//（将来有人给它的白名单加了写工具，运行期直接拒绝）。
		ID: "researcher", Name: "调研 Agent", Enabled: true, Color: "#10a37f", Model: "",
		Desc:     "代码库与资料勘察：全文检索、历史会话与跨文件脉络梳理，只给结论与出处。",
		Prompt:   "你是调研 Agent。只做检索与信息整理：结论必须带依据（文件路径 + 行号、命令输出或文档链接）；查不到就如实说「未找到」，不编造也不推测。不修改任何文件。",
		Tools:    []string{"read_file", "search", "session_search", "ripgrep"},
		Workflow: "research-first", Skills: []string{},
		Delegates: []string{}, Approval: "strict",
	},
	{
		// 验证面：bash 是它的核心能力（不跑就无从验证），edit/write_file 留给
		// 「按验收标准补用例」；提示词把项目的测试纪律写进职责（不许为绿灯弱化断言）。
		ID: "tester", Name: "测试 Agent", Enabled: true, Color: "#0ea5e9", Model: "",
		Desc:     "验证与测试：跑构建与测试、按验收标准补用例，如实回传失败与复现命令。",
		Prompt:   "你是测试 Agent。跑构建与测试，断言行为与契约而不是实现细节；失败如实回传（关键输出 + 复现命令）。绝不为了让测试变绿而删除、跳过或弱化断言——测试与实现冲突时先判断谁对：实现错了改实现，需求变了先改方案。",
		Tools:    []string{"read_file", "search", "edit", "write_file", "bash", "todo"},
		Workflow: "plan-execute-verify", Skills: []string{},
		Delegates: []string{}, Approval: "confirm",
	},
}
