// 种子数据（首次建库注入；内容与前端 agent-seeds.ts 的目录部分对齐——
// 演示 Agent 名单后端给主 + 代码 + 调研 + 测试 + lxagent（配置维护）五个
// 可委派项，外加合并进程拉起的 merger（不进委派名单）；其余（审查、运维）
// 是前端演示专属，真实后端不替用户预置）。
// 新增种子 Agent 的落地纪律见 catalog_seeds.go 的 syncCatalogSeeds：缺失才插入，
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
		// 网页搜索（M4）：**必须在目录里**，否则 Agent 编辑器渲染不出它的
		// chip（内置 chips 来自目录，见 AgentEditor 的 builtinToolChips），
		// 用户就没法把 web_search 勾给任何 Agent —— 而默认会话用的就是
		// 主 Agent（白名单只有 agent_dispatch），结果是这个工具做完了却
		// 谁也用不上。
		ID: "web_search", Desc: "联网搜索网页（多渠道，主渠道失败自动降级）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "query", Type: "string", Required: true, Desc: "搜索查询词（自然语言问题即可）"},
			{Name: "num_results", Type: "int", Desc: "结果条数（默认 5，上限 20）"},
			{Name: "recency", Type: "enum", Desc: "时间范围：day / week / month / year"},
			{Name: "domains", Type: "array", Desc: "限定域名；前缀 - 表示排除"},
		},
		Doc: "查最新信息、文档、报错、API 用法等可能过时的事实。\n\n- 不要用它搜本仓库代码（那用 search）\n- 多个渠道按主渠道优先自动降级；失败会说明是哪个渠道出的错\n- 「搜到 0 条」和「搜索失败」是两种结论——前者换关键词，后者如实报告\n\n渠道配置见「设置 → 网页搜索」。",
	},
	{
		// 与 web_search 同理必须在目录里：编辑器渲染内置 chips 靠目录，
		// 不在目录里用户就勾不上它（web_search 的实际遭遇，见上面那条注释）。
		ID: "web_fetch", Desc: "抓取网页正文（HTML 转文本，禁内网）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "url", Type: "string", Required: true, Desc: "http/https 地址（先用 web_search 找到它）"},
			{Name: "max_chars", Type: "int", Desc: "正文上限（默认 20000，上限 80000）"},
		},
		Doc: "web_search 只回标题与摘要，**要看全文用这个**。\n\n- 先用 web_search 找到地址，再抓正文\n- 只支持 http/https，**禁止访问本机与内网地址**（环回/私有网段/云元数据端点）\n- 正文超上限会截断（可调 max_chars）；纯 JS 渲染的页面可能抓不到正文\n- 二进制内容（图片/PDF）如实报类型，不灌乱码进上下文",
	},
	{
		// 后台任务三件套（docs/jobs.md §3）：bash 的 run_in_background 起任务，
		// 这三个读输出/列任务/停任务。同样必须在目录里——编辑器渲染 chip 只读目录，
		// 目录里没有 = 用户在界面上勾不到它（web_search 栽过的那一跤）。
		ID: "job_output", Desc: "读后台任务的输出（增量；可阻塞等结束）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "job_id", Type: "string", Required: true, Desc: "任务 id（run_in_background 返回或 job_list 里的）"},
			{Name: "wait", Type: "bool", Desc: "true = 阻塞等它结束（默认 30s，上限 600s）"},
			{Name: "timeout_ms", Type: "int", Desc: "wait 的等待上限毫秒数（默认 30000，上限 600000）"},
			{Name: "max_bytes", Type: "int", Desc: "单次读取字节上限（默认 32768）"},
		},
		Doc: "默认只回**上次读过之后的新增输出**（无新输出回 (no new output)）。\n\n- wait=true 阻塞等它结束：适合「等构建/测试跑完再继续」\n- 超时返回当前输出 + [status: running]，据此判断它还在跑\n- 任务结束后仍可读（完整日志落在 OutputPath，可用 read_file 看全文）",
	},
	{
		ID: "job_list", Desc: "列后台任务（新的在前）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "session_only", Type: "bool", Desc: "true = 只列本会话（默认全部会话）"},
		},
		Doc: "列出任务 id、状态、命令摘要与结束方。\n\n- 默认列**全部会话**的任务：跨会话常驻的 dev server 也该看得见\n- 结束方（self/user/agent/backend）区分「它自己退的 / 用户停的 / 我停的 / 后端重启中断」",
	},
	{
		ID: "job_kill", Desc: "停止一个后台任务（归属记为 agent）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "job_id", Type: "string", Required: true, Desc: "要停止的任务 id"},
		},
		Doc: "非阻塞：立刻回「已请求取消」，真正的结束由进程收尾时定稿。\n\n- 归属记为 agent：唤醒通告据此措辞，不会把自己停的任务当成「它挂了」\n- 自己停掉的任务**不要**自己重启，要等用户指示",
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
		// bash_readonly（只读执行面）：调研 Agent 读本地数据的手脚——查 SQLite
		// 聚合、看 git 历史、统计文件规模都靠它。同样必须在目录里：Agent 编辑器
		// 渲染内置 chip 只读目录，目录里没有 = 用户勾不上它（web_search 栽过的那一跤）。
		// 判据与拒绝理由见 internal/tools/readonly.go。
		ID: "bash_readonly", Desc: "执行只读 shell 命令（读数据：ls/grep/wc、git log、sqlite3 查询）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "command", Type: "string", Required: true, Desc: "只读命令（ls / cat / grep / find / wc / git log / sqlite3 的 SELECT 等）"},
			{Name: "cwd", Type: "string", Desc: "工作目录（绝对或相对会话工作目录，可选）"},
			{Name: "stdin", Type: "string", Desc: "标准输入（可选，≤64KB）"},
			{Name: "timeout_sec", Type: "int", Desc: "超时秒数（默认 60，上限 300）"},
		},
		Doc: "只读执行面：命令级白名单校验，写操作（rm/mv/重定向/git commit/git push/包管理/解释器脚本）一律拒绝并说明原因；命令替换、解释器等无法静态判定的结构同样拒绝（只读命令之间的管道可以）。\n\n- 适合读数据：查 SQLite、看 git 历史、统计文件规模\n- 改文件请交给代码 Agent（bash / edit / write_file）",
	},
	{
		ID: "todo", Desc: "任务清单全量写入", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "items", Type: "array", Required: true, Desc: "[{content, status: pending|active|done}]"}},
		Doc:    "多步任务的过程对齐——每完成一步更新状态，清单是唯一事实源；active 项唯一。",
	},
	{
		// 确认门的「提问」形态：与 todo 同理必须在目录里（编辑器渲染 chip 只读
		// 目录，目录里没有 = 用户在界面上勾不到它）。
		ID: "ask_user", Desc: "向用户提问并等待回答（冲突抉择时用）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "question", Type: "string", Required: true, Desc: "要问用户的问题（说清背景、取舍与你的建议）"},
			{Name: "options", Type: "array", Desc: "预设答案（最多 6 个，用户可直接点选）"},
		},
		Doc: "向用户提问并**挂起等待回答**，回答文本作为工具结果回填。\n\n- 问题要自包含：说清背景与两边的意图，给出你的建议与理由\n- 用户可能跳过（「用户没有回答」）——那时自行决策或换做法，别反复重问\n- 纯机械的取舍（不同文件、不同区域）自己解决，不要每处都问",
	},
	{
		ID: "session_search", Desc: "搜历史会话内容（带标题、时间与上下文）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "pattern", Type: "regex", Required: true},
			{Name: "max", Type: "int", Desc: "最多返回条数（默认 30，上限 100）"},
			{Name: "context", Type: "int", Desc: "每条命中前后各带几条相邻消息（默认 2，上限 5）"},
			{Name: "role", Type: "enum", Desc: "只搜某个角色：user / assistant / tool"},
		},
		Doc: "跨全部会话的消息内容检索（含当前），按会话时间从近到远。\n\n- 命中带**会话标题与时间**（判断是哪个会话的事）\n- 默认附前后各 2 条相邻消息：「怎么修的」通常就在命中后面几条\n- role 过滤只作用于命中判定，上下文里仍能看到其它角色\n- 命中数超过 max 时会提示总数——据此缩小 pattern",
	},
	{
		// 内置的渐进披露读取口：目录里没有它，子 Agent 就「拿到技能索引却没法取正文」
		// （技能多选注入因此形同虚设）——用户拍板顺手补上。
		ID: "read_skill", Desc: "读取技能模块的完整内容（提示词只列索引）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "id", Type: "string", Required: true, Desc: "技能 id（提示词「可用技能」清单里的名字）"}},
		Doc:    "渐进披露：提示词只注入技能索引（id + 摘要），需要完整方法论时按 id 取全文。\n\n没在白名单里的技能读不到（提示词里看不到 = 不存在）。",
	},
	{
		// share_file（附件展示通道）：把工作目录里的产物共享到对话（前端附件卡）。
		// 同样必须在目录里：Agent 编辑器渲染 chip 只读目录，目录里没有 = 用户
		// 勾不上它（web_search 栽过的那一跤）。
		ID: "share_file", Desc: "把工作目录里的文件共享到对话（图片缩略图/文件卡）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "path", Type: "string", Required: true, Desc: "文件路径（相对会话工作目录；必须在工作目录之内）"},
			{Name: "caption", Type: "string", Desc: "一句话说明（可选）：文件是什么、用户该看哪里"},
		},
		Doc: "只读语义：读源文件、复制进会话附件目录、渲染成附件卡——不修改任何文件。\n\n- 适合展示你刚生成的产物（图表、报告、导出数据、截图）\n- 图片（png/jpeg/webp/gif）渲染为可点击放大的缩略图，其余是可下载的文件卡\n- 单文件 ≤20MB；路径越出工作目录会被拒绝",
	},
	{
		// read_image（视觉读图通道）：读一张图给视觉模型看。同样必须在目录里：
		// Agent 编辑器渲染 chip 只读目录，目录里没有 = 用户勾不上它（web_search
		// 栽过的那一跤）。
		ID: "read_image", Desc: "读取图片内容给视觉模型看（须声明「视觉」能力）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{{Name: "path", Type: "string", Required: true, Desc: "图片路径（相对会话工作目录；必须在工作目录之内）"}},
		Doc:    "只读语义：读工作目录里的图片（png/jpeg/webp/gif），作为视觉输入注入下一轮请求——模型直接描述与分析图里的内容。\n\n- 当前模型必须声明「视觉」能力，否则报人话错误\n- 给用户看图用 share_file（本工具只喂模型，不渲染给用户）\n- 单图 ≤5MB；路径越出工作目录会被拒绝",
	},
	{
		// agent_read / agent_write（Agent 配置读写，lxagent 的手脚）。与
		// agent_dispatch 同理必须在目录里：lxagent 的种子白名单直接写着它们，
		// 且编辑器渲染 chip 也只读目录——目录里没有 = 用户勾不上、界面上看不到。
		ID: "agent_read", Desc: "查看 Agent 注册表（列表或单个完整定义，含提示词模板）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "id", Type: "string", Desc: "要读的 Agent id（可选；缺省 = 列出全部）"},
		},
		Doc: "读 Agent 配置：不带 id 列出全部（id/名称/职责/白名单/技能），带 id 读单个完整定义（含模板全文）。\n\n- 修改 Agent 配置前先读现状——改之前知道它现在长什么样\n- 列表视图不含提示词全文（上下文考虑），要看全文带 id 单读",
	},
	{
		ID: "agent_write", Desc: "按 id 修改 Agent 配置（描述/模板/白名单/技能绑定，高危确认门）", Risk: "high", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "id", Type: "string", Required: true, Desc: "目标 Agent 的 id（不可改）"},
			{Name: "desc", Type: "string", Desc: "新的职责描述（可选）"},
			{Name: "prompt", Type: "string", Desc: "新的系统提示词模板全文（可选；不能为空）"},
			{Name: "tools", Type: "array", Desc: "新的工具白名单（可选；全量替换，不能清空，名字必须真实存在）"},
			{Name: "skills", Type: "array", Desc: "新的技能绑定（可选；全量替换，可为空）"},
		},
		Doc: "字段级更新：只写提供的字段，其余不动；id 不可改。\n\n- **高危**：改动落库即生效——该 Agent 下次被派发就按新配置运行\n- 工具白名单增删直接改变目标 Agent 能做什么（加写工具 = 它将能改动文件）\n- 硬校验：白名单不能清空、工具名必须真实存在、模板不能为空、主 Agent 不可改\n- 改之前先 agent_read 摸现状，向用户确认意图",
	},
	{
		// 调度通道（主 Agent 的唯一工具）。与 web_search 同理必须在目录里：
		// 主 Agent 的种子白名单直接写着它，但编辑器渲染 chip 也要靠目录
		//（否则界面上看不到自己有哪些工具）。
		ID: "agent_dispatch", Desc: "把任务派给名单中的子 Agent（子 Agent = 独立会话）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "agent", Type: "string", Required: true, Desc: "目标 Agent 的名单 id"},
			{Name: "task", Type: "string", Required: true, Desc: "任务描述（目标 + 约束 + 验收标准）"},
			{Name: "context", Type: "string", Desc: "可选背景（不是完整历史）"},
			{Name: "session", Type: "string", Desc: "可选：续跑既有子会话的 id"},
		},
		Doc: "主 Agent 只做决策与分派，不直接执行任务。\n\n- 任务描述必须**自包含**——子 Agent 看不到当前对话历史\n- **并行**：一条消息里发多个 dispatch 调用，它们**真的并行跑**（子会话各自独立）；有前后依赖的才分多轮串行\n- 子 Agent 在独立会话里执行（有自己的历史与压缩），返回值带子会话 id\n- 要接着上次进度继续：把子会话 id 填进 session\n- 两类制：子 Agent 的白名单不含它（委派深度恒 1）",
	},
	{
		// 合并进程（第二个 producer）的入口工具。与 agent_dispatch 同理必须在目录里：
		// 编辑器渲染 chip 只读目录，目录里没有 = 用户在界面上勾不到它。
		ID: "merge_request", Desc: "起一个合并进程（把本会话改动交给合并 Agent 汇总）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "target_branch", Type: "string", Desc: "目标分支（可选；默认 lxcode/integration）"},
		},
		Doc: "起一个**后台合并进程**：任务体是内置的合并 Agent，在集成分支的专用工作树里把本会话分支的改动汇总进去（处理冲突、跑构建测试）。\n\n- 立刻返回任务 id；合并结束后经唤醒投递自动通告你（用 job_output 读输出）\n- 未分组会话没有可合并的分支（先归入项目）\n- 同一会话同时只允许一个在跑的合并进程",
	},
	{
		// 工作区三件套之一（查询面，低危只读）。必须在目录里：编辑器渲染 chip
		// 只读目录，目录里没有 = 用户在界面上勾不到它（web_search 栽过的那一跤）。
		ID: "workspace_status", Desc: "查询项目工作区状态（主检出改动、各会话分支、集成分支）", Risk: "low", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "project_id", Type: "string", Desc: "项目 id（可选；默认当前会话归属的项目）"},
		},
		Doc: "只读汇总：主检出的未提交/未跟踪文件、各会话分支领先多少（有没有工作树、是否已并入主检出）、集成分支的领先/落后。\n\n- 用户问「本地改了哪些东西」用它\n- 全部只读，绝不动工作区\n- 未分组会话报「没有归属项目」",
	},
	{
		// 工作区三件套之二（提交推送链路，中危走确认门）。
		// Desc/Doc 必须说实际行为：2026-10 直连架构下就是「提交当前分支 + 直接推送」，
		// 没有合并进程也没有集成分支（那套已退役）。
		ID: "workspace_sync", Desc: "提交当前分支的改动并直接推送到 origin（可选）", Risk: "high", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "message", Type: "string", Desc: "提交信息（可选；默认取最近一条用户消息首行）"},
			{Name: "push", Type: "bool", Desc: "提交后是否把当前分支直接推送到远程 origin（默认 false）"},
		},
		Doc: "「帮我提交/推送」的执行链路：提交主检出当前分支 →（push=true 时）直接推送到 origin（不 force）。\n\n- 工作区干净则跳过提交（不报错）\n- 推送失败不影响已完成的提交（如实报错，提交不会回退）\n- **lxcode 本体仓库的推送被闸门拒绝**：本地 master 含历史逐轮检查点，发版走 `node scripts/push-release.mjs [version]`（提交照常）\n- 未分组会话报「没有归属项目」",
	},
	{
		// 工作区三件套之三（回滚，中危走确认门）。
		ID: "workspace_rollback", Desc: "回滚主检出当前分支（上一个提交/会话起点/指定提交）", Risk: "high", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "target", Type: "enum", Desc: "last-turn（默认）/ session-start / 原始 commit hash"},
		},
		Doc: "「回滚到上一次提交」的执行者：把主检出当前分支 reset --hard 到目标提交。\n\n- 轮末自动检查点已移除（2026-10 用户拍板）：只有经 workspace_sync 或手工提交过的位置可回滚，last-turn = 上一个提交（不是上一轮对话）\n- 工作区有未提交改动时拒绝（绝不静默丢弃手工改动）\n- 目标提交已合并进集成分支时警告：那部分要在集成分支上 revert 才能撤销\n- 绝不 push、绝不动集成分支与主检出\n- 执行前走确认门（文案列明丢弃哪些提交与文件）",
	},
	{
		// 产物发布（中危走确认门）：会话 worktree 里的构建产物在 gitignore 里、
		// 不随合并走——用户的项目文件夹里看不到它们；这个工具把产物显式送到
		// 主检出对应位置。必须在目录里：编辑器渲染 chip 只读目录。
		ID: "workspace_publish", Desc: "把会话工作树里的产物复制到项目主检出", Risk: "high", Source: "builtin", Custom: false,
		Params: []sessiondata.ToolParam{
			{Name: "source", Type: "string", Required: true, Desc: "会话工作树内的相对路径（文件或目录；绝对路径拒绝）"},
			{Name: "target", Type: "string", Desc: "主检出内的相对路径（默认与 source 相同）"},
			{Name: "exclude", Type: "array", Desc: "目录同步时排除的目录名（.git 始终自动排除）"},
		},
		Doc: "把会话工作树里的产物（构建产物、生成的文件或目录）复制到项目主检出对应位置，让产物出现在用户的项目文件夹里。\n\n- source/target 都必须是相对路径（绝对路径与 `..` 拒绝）\n- 目录递归复制、保留相对结构；目标父目录不存在会自动创建\n- 覆盖主检出已有文件前走确认门（确认文案列出会覆盖的文件）\n- 单次上限 2000 个文件 / 1GB，超出报「产物过大，请分批」",
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
	{
		// 合并进程的内置流程（merger 的 Workflow）：合并纪律是硬约束，不是可选项
		//（不用 --force/-X 掩盖冲突、失败不谎报成功）。
		ID: "merge-verify", Kind: "process", Custom: false,
		Desc: "合并 → 验证：先看两边改动再合并，冲突逐个解决，合并后跑构建测试",
		Body: "# 合并 → 验证\n\n在集成分支的工作树里把源分支的改动汇总进来。\n\n## 先看再合\n- 合并前先看两边改了什么（git log / git diff 源分支与目标分支）\n- 明白两边的意图再动手，不盲目 merge\n\n## 解冲突\n- 冲突逐个解决并说明取舍：为什么保留这一边，另一边的意图如何被满足\n- **绝不用 --force、-X theirs / -X ours 掩盖冲突**——那是把别人的改动悄悄丢掉\n\n## 验证\n- 合并后跑构建与测试，按仓库的验收标准验证\n- 失败就把集成分支恢复原状并**如实报告**，绝不谎报成功\n- 不 push、不动主检出、不丢弃任何人的改动",
	},
}

var seedAgents = []sessiondata.AgentDef{
	{
		ID: "main", Name: "主 Agent", IsMain: true, Enabled: true, Color: "#0d0d0d", Model: "",
		Desc: "决策与分派中枢：理解意图、拆解任务、调用名单中的 Agent 并验收汇总。不直接执行任务。",
		// 工作区四件套是主 Agent 的「用户无感」入口：查询 / 提交推送 / 回滚 /
		// 产物发布——用户视角始终在操作一个项目，分支概念由这些工具兜住。
		Tools:    []string{"agent_dispatch", "merge_request", "workspace_status", "workspace_sync", "workspace_rollback", "workspace_publish"},
		Prompt:   "",
		Workflow: "plan-execute-verify", Skills: []string{},
		Delegates: []string{"coder", "researcher", "tester", "lxagent"}, Approval: "confirm",
	},
	{
		// 职责边界（2026-12 用户拍板）：**coder 只负责写代码，不跑测试**——测试与
		// 验证是 tester 的活。所以 Desc/Prompt 里不出现「跑构建测试」（Desc 是主
		// Agent 的选人信号，写着「跑测试」会让主 Agent 把验证任务也派给 coder），
		// 只保留「改完确认能编译/无语法错误」这一条最低限度的自检底线：写出的代码
		// 连编译都过不了就交出去，等于把最基本的收尾甩给下游。
		ID: "coder", Name: "代码 Agent", Enabled: true, Color: "#3b82f6", Model: "",
		Desc:     "写代码：新增功能、修 bug、重构与改配置（只改文件：测试交给测试 Agent、查资料交给调研 Agent），能把产物文件共享到对话、能读图片内容（视觉）。",
		Prompt:   "你是代码 Agent。改动前先读相关代码，遵守仓库规范；每步改动可解释、可回退。你只负责实现与重构，**不跑测试**——验证（跑构建与测试、补用例、验收）是测试 Agent 的职责，把改动与改动说明如实交回即可；也不需要做大面积资料勘察（那是调研 Agent 的活，任务里没给的信息直接说明缺口）。唯一的自检底线：改完确认代码能编译、无明显语法错误（如 go build / tsc 级别的编译检查），不要让连编译都过不了的代码流到下游。绝不执行 git push（含 --force）：推送只由用户经 workspace_sync 发起；lxcode 本体仓库的发版只走 scripts/push-release.mjs。",
		Tools:    []string{"read_file", "search", "read_image", "edit", "write_file", "bash", "todo", "share_file"},
		Workflow: "minimal-change", Skills: []string{"frontend-design", "gsap"},
		Delegates: []string{}, Approval: "confirm",
	},
	{
		// 调研面：只读工具集（无 edit/write_file），所以「不改任何文件」是结构
		// 保证而不是靠提示词自觉；approval=strict 是同一件事的第二道保险
		//（将来有人给它的白名单加了写工具，运行期直接拒绝）。
		// web_search 是调研的核心能力之一（本仓库代码之外的资料都靠它），
		// 且它是只读工具——strict 档下照样可用（Mutates=false）。
		//
		// bash_readonly 自 2026-12 起在调研面上（用户反馈：没有执行面就读不了
		// 本地数据——查 SQLite 聚合统计跑不了 sqlite3、看历史跑不了 git log、
		// 统计规模跑不了 wc，只能答「做不到」）。它是**独立的只读工具**
		//（tools/readonly.go）：命令级白名单校验，写操作一律拒绝并回填出路；
		// 自身 Mutates=false，所以在 strict 档下照常可用，也不需要逐条确认
		// （调研查一次库弹一次窗等于不可用）。
		ID: "researcher", Name: "调研 Agent", Enabled: true, Color: "#10a37f", Model: "",
		// Desc 是**主 Agent 的选人信号**（可委派名单按它逐字生成，见 compose.go 的 ④）：
		// 漏写「联网搜索」的后果是主 Agent 不知道「查外部资料」该派给谁——工具给了、
		// 白名单勾了，但选人那一步没有信号，这个能力等于不存在。
		Desc:     "只读调研：检索代码、读文件、查数据库（只读 bash_readonly：sqlite3 查询/git log/wc 等）、联网搜索与抓取网页正文、梳理跨文件脉络，只给结论与出处，不改任何文件。",
		Prompt:   "你是调研 Agent。只做检索与信息整理，**不修改任何文件、不执行构建测试、不提交推送**。\n\n职责边界：\n- 结论必须带依据：文件路径 + 行号、命令输出或文档链接；查不到就如实说「未找到」，不编造也不推测。\n- bash_readonly 是只读执行面：可以跑读取类命令（ls/cat/grep/find/wc、git log/diff/status、sqlite3 的 SELECT 查询等），写操作（rm/mv/重定向/git commit/git push/包管理/解释器脚本）会被工具直接拒绝。\n- 被拒绝时不要绕过：把「需要什么改动」写进结论，交回主 Agent 派给代码 Agent（coder）或测试 Agent（tester）。",
		Tools:    []string{"read_file", "search", "session_search", "ripgrep", "web_search", "web_fetch", "bash_readonly"},
		Workflow: "research-first", Skills: []string{},
		Delegates: []string{}, Approval: "strict",
	},
	{
		// 验证面：bash 是它的核心能力（不跑就无从验证），edit/write_file 留给
		// 「按验收标准补用例」；提示词把项目的测试纪律写进职责（不许为绿灯弱化断言）。
		// 与代码 Agent 的分工（2026-12 用户拍板）：**测试与验证归这里**——coder 只写
		// 代码不跑测试，所以描述里明确「代码 Agent 交回的改动由你跑构建与测试验收」，
		// 主 Agent 的选人信号据此把验证任务派到这里而不是 coder。
		ID: "tester", Name: "测试 Agent", Enabled: true, Color: "#0ea5e9", Model: "",
		Desc:     "验证与测试：跑构建与测试（代码 Agent 交回的改动由你验收）、按验收标准补用例，如实回传失败与复现命令（不改实现；查资料交给调研 Agent），能把产物文件共享到对话、能读图片内容（视觉）。",
		Prompt:   "你是测试 Agent，负责验证与测试（代码 Agent 只写代码、不跑测试，改动交回后由你跑构建与测试验收）。跑构建与测试，断言行为与契约而不是实现细节；失败如实回传（关键输出 + 复现命令）。绝不为了让测试变绿而删除、跳过或弱化断言——测试与实现冲突时先判断谁对：实现错了改实现，需求变了先改方案；改实现超出「补用例/修测试」的范围时，如实交回主 Agent 派给代码 Agent，不自己重写功能。只读调研（查资料、查历史、梳理跨文件脉络）也不是你的活，派给调研 Agent。绝不执行 git push（含 --force）：推送只由用户经 workspace_sync 发起。",
		Tools:    []string{"read_file", "search", "read_image", "edit", "write_file", "bash", "todo", "share_file"},
		Workflow: "plan-execute-verify", Skills: []string{},
		Delegates: []string{}, Approval: "confirm",
	},
	{
		// 合并进程（第二个 producer）拉起的**内置 Agent**——**不是**主 Agent 的委派对象：
		// 它不在主 Agent 的 Delegates 里（见 seedNonDelegatable），也不是被派活的，
		// 是在集成分支的专用工作树里干活的独立子会话。
		//
		// 白名单含变更类工具（冲突要解、要跑构建测试）；approval=auto（后台进程
		// 没人看着确认门，与 bash 后台任务的语义一致）。
		ID: "merger", Name: "合并 Agent", Enabled: true, Color: "#db2777", Model: "",
		Desc:     "把会话分支的改动汇总到集成分支：处理冲突（冲突抉择可向用户提问）、跑构建测试，如实报告结果。",
		Prompt:   "你是合并 Agent。在集成分支的专用工作树里工作，把源会话分支的改动合并到目标分支。合并纪律：先看两边改了什么（git log / git diff）再合并；冲突必须逐个解决并说明取舍（为什么保留这一边，另一边的意图如何被满足）；绝不用 --force、-X theirs / -X ours 掩盖冲突；合并后跑构建与测试；失败就把集成分支恢复原状并如实报告，绝不谎报成功；不 push、不动主检出、不丢弃任何人的改动。遇到冲突抉择（两边都改了同一处、语义取舍说不清）时，调用 `ask_user` 向用户提问：说明冲突文件、两边的改动意图，给出你的建议与理由；用户回答后按回答执行。纯机械的冲突（不同文件、不同区域）自行解决，不要每处都问。",
		Tools:    []string{"read_file", "search", "edit", "write_file", "bash", "ask_user"},
		Workflow: "merge-verify", Skills: []string{},
		Delegates: []string{}, Approval: "auto",
	},
	{
		// lxagent：**专门维护其他 Agent 配置**的内置 Agent——改系统提示词模板、
		// 工具白名单与技能绑定。它的读写口就是 agent_read / agent_write 两个
		// 工具（store 直改，与前端编辑器同一套校验）；改动过确认门、落库即生效。
		// 审批档 confirm：agent_write 是高危工具，确认门在 confirm 档下生效——
		// 配置变更必须用户点头。
		ID: "lxagent", Name: "lxagent", Enabled: true, Color: "#7c3aed", Model: "",
		// Desc 是**主 Agent 的选人信号**：要说清「改 Agent 配置」该派给谁。
		Desc:     "改 Agent 配置：查看与修改系统提示词模板、工具白名单与技能绑定（只改配置，不写业务代码、不跑测试），改动经确认门落库，对后续派发生效。",
		Prompt:   "你是 lxagent，专门维护其他 Agent 的配置：修改系统提示词模板、工具白名单与技能绑定。\n\n职责边界：\n- 只改 Agent 配置，不替用户做业务决策，也不亲自执行目标 Agent 的业务任务（不写业务代码、不跑测试、不做调研——那些派给 coder / tester / researcher）。\n- 动手前先 agent_read 摸清现状，向用户确认意图（改哪个 Agent、改成什么、为什么）再调用 agent_write。\n- agent_write 是高危操作（影响之后所有派发）：白名单加写工具会让目标 Agent 获得改动文件的能力，撤技能绑定会改变它的方法论——把后果说清让用户知情。\n- 每次改动后用 agent_read 复读确认落库结果，向用户回报改了什么。\n\n硬约束（工具层强制，勿试图绕过）：id 不可改、白名单不能清空、模板不能为空、工具名必须是工具目录里真实存在的、主 Agent 的配置不可经此修改。",
		Tools:    []string{"agent_read", "agent_write", "read_file", "search", "session_search", "ask_user", "todo"},
		Workflow: "minimal-change", Skills: []string{},
		Delegates: []string{}, Approval: "confirm",
	},
}
