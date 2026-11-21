// 窗口化装载（2026-12 会话切换优化批 4）：切会话不全量装载历史。
//
// 背景（实测依据，server/session_ops.go 的性能审计注释）：恢复一条大会话 =
// newRuntime → AttachTo → st.Load 全量路径读（大会话几十毫秒到秒级），而
// chat.history(tail=240) 的首屏只需要尾部窗口。窗口化附着让运行时先只持有
// 尾部 n 行（store.LoadTail 的 SQL 深度上限截断——不扫路径前缀的行正文），
// 首屏装载与 session.resume 都快掉一个数量级；**第一次真正需要全量历史时**
// （发送/撤回/切叶/压缩）再补载——补载读的是同一张表，总成本不变，只是把
// 它从「切会话的关键路径」挪到「真要用的时候」。
//
// 纪律：
//   - **TailLoader 是可选扩展**（接口缺席回退全量 AttachTo，零破坏内存测试桩）；
//   - **补载是 fail-closed 的**：Send/Rewind/SetLeaf/Compact 入口补载失败就
//     拒绝动作（历史不全就动历史 = 撕裂内存与库的一致性）；
//   - **窗口态的派生值缓存照常工作**（bumpHistoryLocked）：配对切点/占用估算
//     在窗口上算——配对只服务「窗口内能不能切」（补载前不可能被要求切窗口外，
//     Rewind/SetLeaf 入口先补载）；占用估算按窗口偏小（真实测量优先，库里
//     落过测量的会话不受影响；首次发轮后由 provider 真实用量重锚）。
package agent

import (
	"errors"
	"fmt"

	"github.com/moyunteng/lxcode/internal/llm"
)

// TailLoader 是 Persistence 的**可选**扩展：支持尾部/分页窗口装载
// （会话切换优化批 4）。实现缺席时 AttachToWindowed 回退全量装载——内存测试桩
// 与自定义实现零破坏。
type TailLoader interface {
	// LoadTail 取当前历史的尾部/分页窗口（surface 顺序，影子/检查点语义与
	// 全量 Load 同一份判定）：before ≤ 0 = 从 leaf 取尾部 n 行；before > 0 =
	// 取该 seq 之前的窗口（分页，不含锚点）。atRoot = 窗口已覆盖到路径根
	//（前面没有更多历史——分页 has_more 的判据）。
	LoadTail(id string, before int64, n int) (msgs []llm.Message, atRoot bool, err error)
}

// tailWindow 常量：窗口化附着时取的尾部行数预算（**行数**，不是 surface 消息
// 数——surface 只少不多，除检查点）。首屏 = 前端 HISTORY_TAIL(240) + 配对回退
// 余量（tailPairingMargin，store 侧常量）；窗口稍大于首屏没有额外成本（读取
// 已经过 SQL 截断），换来的是 TailHistory(n) 的配对回退几乎永远不用重取。
const tailWindowRows = 280

// loadWindowBalanced 取一个**起点已配平**的尾部/分页窗口：带余量取行 → 局部
// 配对回退（与 TailHistory 同一套不变量）→ 回退触到取数边界且前面还有历史时
// 加倍余量重取（有界重试，最终整条路径都在窗口里 = 局部配对与全量配对一致）。
//
// 为什么要有重取：窗口起点要落在全量配对平衡切点上，而窗口局部只能算出
// 「窗口内」的平衡——起点之上的配对状态（有没有横跨取数边界的未回填调用）
// 局部不可知。重取的收敛性：每次余量翻倍，真实历史的未配对横跨段是单轮工具
// 调用（几十条），两跳内必然覆盖；覆盖后回退点落在窗口内部 = 局部判定精确。
// 取到根（atRoot）时局部判定**恒精确**（没有边界外的前缀）。
func loadWindowBalanced(tl TailLoader, id string, before int64, want int) ([]llm.Message, bool, error) {
	fetch := want + tailPairingMarginAgent
	var msgs []llm.Message
	atRoot := false
	for attempt := 0; ; attempt++ {
		var err error
		msgs, atRoot, err = tl.LoadTail(id, before, fetch)
		if err != nil {
			return nil, false, err
		}
		// 回退目标：尾部窗口从「want 条之前」回退；分页窗口就是整段（want=len）。
		start := len(msgs) - want
		if start < 0 {
			start = 0
		}
		pairing := AnalyzeToolPairing(msgs)
		cut := pairing.NearestBalancedAtOrBefore(start)
		switch {
		case cut > 0:
			// 回退点在窗口内部：局部判定精确（前面还有 ≥1 条未被跨越的平衡切点
			// 作缓冲——见上方收敛性讨论），直接切。回退点之前还有历史 → atRoot=false
			//（取数层的 atRoot 只描述取数覆盖，窗口语义按最终起点重判）。
			return msgs[cut:], false, nil
		case cut == 0 && atRoot:
			// 窗口已到根：局部 = 全量，起点（含全量）就是安全切点。
			return msgs, true, nil
		case cut == 0 && attempt >= 3:
			// 有界重试耗尽（横跨段 >余量×8 的畸形历史）：如实取当前窗口——
			// 配对回退退化，但返回的窗口仍是 surface 一致的合法子集。
			return msgs, atRoot, nil
		default:
			// 回退触到取数边界且前面可能还有历史：加倍余量重取。
			fetch *= 2
		}
	}
}

// tailPairingMarginAgent 是 agent 侧的配对回退余量（与 store.tailPairingMargin
// 同值——两边各算一遍会漂移，但 agent 不能 import store（分层），store 不能
// import agent（分层反向）。同一条注释钉住：改一处必须同步另一处）。
const tailPairingMarginAgent = 40

// Windowed 返回本会话是否处于窗口化装载态（内存里只有尾部窗口）。
// 服务端的轻量探测用它（prepareWorkspace 的「有无历史」前置检查——窗口态下
// len(History().Messages) 与全量同真值：窗口非空 ⇔ 历史非空）。
func (s *Session) Windowed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.windowed
}

// HasMessages 返回当前会话是否有历史消息（**轻量探测**——不复制消息切片、
// 窗口态不触发全量补载）：历史非空 ⇔ 窗口非空（窗口是历史的尾部，长度 ≥1）。
// server 的 prepareWorkspace 前置检查用它（那个检查只回答「要不要准备工作区」，
// 不需要消息本体）。
func (s *Session) HasMessages() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.history) > 0
}

// AttachToWindowed 把会话挂到指定存储与既有会话 id，**只装载尾部窗口**
// （AttachTo 的窗口化变体——参数与提交语义逐条一致，差异只有取数）。
// st 不支持 TailLoader 时与 AttachTo 等价（全量装载——降级，不是错误）。
func (s *Session) AttachToWindowed(st Persistence, id string) error {
	if st == nil {
		return errors.New("持久化实现为空")
	}
	if id == "" {
		return errors.New("会话 id 不能为空")
	}
	tl, ok := st.(TailLoader)
	if !ok {
		return s.AttachTo(st, id) // 降级：实现不支持窗口，全量装载
	}
	msgs, _, err := loadWindowBalanced(tl, id, 0, tailWindowRows)
	if err != nil {
		return err
	}
	ws, err := st.WorkspaceOf(id)
	if err != nil {
		return err
	}
	dir, err := resolveWorkspaceDir(st, ws)
	if err != nil {
		return err
	}
	s.mu.Lock()
	agentID := s.agentID // 窗口按归属 Agent 的模型解析（子会话可以绑自己的模型）
	s.mu.Unlock()
	usage := s.restoredUsage(st, id, agentID, msgs)
	approval := restoredApproval(st, id)
	model := restoredSessionModel(st, id)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return fmt.Errorf("%w（不能附着到别的会话）", ErrBusy)
	}
	s.st, s.id, s.history, s.workDir = st, id, msgs, dir
	s.windowed = true
	s.workspace = ws
	s.todos, s.pendingWorkspace = restoredTodos(st, id), ""
	s.context = usage
	s.approval = approval
	s.model = model
	s.bumpHistoryLocked() // 装载了新历史（尾部窗口）：派生值缓存失效（history_cache.go）
	return nil
}

// ensureFullHistory 窗口态补载全量（Send/Rewind/SetLeaf/Compact/全量 History
// 的入口前置——历史不全就动历史会撕裂内存与库）。非窗口态是 no-op。
//
// 锁纪律：读库在锁外（与 AttachTo/SwitchTo 同一条），提交持锁。
// 补载失败返回错误（fail-closed——调用方必须拒绝后续动作，不能猜）。
// 幂等：并发双发时后到者发现 windowed 已 false，直接返回。
func (s *Session) ensureFullHistory() error {
	s.mu.Lock()
	if !s.windowed {
		s.mu.Unlock()
		return nil
	}
	st, id := s.st, s.id
	s.mu.Unlock()
	if st == nil || id == "" {
		return errors.New("窗口态会话缺少存储上下文（内部错误）")
	}
	msgs, err := st.Load(id) // 全量路径（AttachTo 同一个取数口）
	if err != nil {
		return fmt.Errorf("补载会话全量历史失败: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 期间可能已被补载过（并发）：以先到者为准——两份 Load 读的是同一张表
	// 同一叶指针，内容一致；直接覆盖也安全，但保留原引用省一次派生缓存失效。
	if !s.windowed {
		return nil
	}
	s.history = msgs
	s.windowed = false
	s.bumpHistoryLocked() // 补载了全量：派生值缓存失效（history_cache.go）
	return nil
}
