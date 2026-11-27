// AI 自动命名会话（B1）：首轮自然收尾（finishReason=stop、无工具调用）后，
// 标题仍是「首条 user 消息的默认截断」时，用当前模型发一次小请求生成 4-8 字
// 中文短标题，经 store.Rename 落库并广播（宿主把 TitleRenamedEvent 映射成
// session.changed(renamed)——侧栏自动刷新）。
//
// 纪律：
//   - **只命名一次**（titled 置位先于请求——失败不重试：失败重试会在每个收尾
//     轮再打一次端点，收益配不上噪声；用户改名后标题 ≠ 默认值，判定天然不再触发）；
//   - **异步**（go routine + 独立 ctx 超时）：命名不阻塞 done 事件与下一轮；
//   - **失败静默**：命名是锦上添花，端点报错/超时/空回复都回落默认标题；
//   - **竞争守卫**：改名前再读一次库标题——用户此刻已手动改名（≠默认值）就放弃
//     （落库侧没有乐观锁，读-判-写的窗口以「再读一次」收窄到足够小）；
//   - **只在顶层会话**：子会话标题不进侧栏（dispatch.go 过滤），不命名。
package agent

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// autoTitleTimeout 是命名调用的超时（独立短档——标题不值得等长响应）。
const autoTitleTimeout = 30 * time.Second

// autoTitlePromptMaxTokens 是命名回复的输出上限（4-8 个汉字绰绰有余）。
const autoTitlePromptMaxTokens = 48

// autoTitleSystemPrompt 是命名调用的提示词。
const autoTitleSystemPrompt = "你是会话命名器。根据用户的首条消息生成一个 4-8 个汉字的中文短标题，" +
	"概括这段对话要做的事。只输出标题本身：不要引号、不要句号、不要任何解释。"

// autoTitleUserPrefix 是命名请求里 user 消息的前缀（防注入：把用户原文当**数据**
// 包起来，不当作指令）。
const autoTitleUserPrefix = "用户的首条消息：\n"

// TitleRenamedEvent：AI 自动命名完成（标题已落库）——宿主据此广播
// session.changed(renamed) 刷新侧栏列表。
type TitleRenamedEvent struct {
	ID    string
	Title string
}

func (TitleRenamedEvent) isEvent() {}

// maybeAutoTitle 是 runTurn 收尾的钩子：判定条件全过才异步发起命名。
// workDir 未用（占位对齐其他轮末钩子的形态；命名不依赖工作目录）。
func (s *Session) maybeAutoTitle(_ string) {
	if !s.autoTitle {
		return
	}
	s.mu.Lock()
	// 只命名一次（含失败——置位先于异步请求，重复收尾不再发起）。
	if s.titled || s.protectHead || s.st == nil || s.id == "" {
		s.mu.Unlock()
		return
	}
	// 首条 user 消息的默认截断（与 store 落库首条时同一份实现——sessiondata 收口）。
	var firstUser string
	for _, m := range s.history {
		if m.Role == "user" && !m.Notice {
			firstUser = m.Content
			break
		}
	}
	if firstUser == "" {
		s.mu.Unlock()
		return // 空会话/纯通告轮——没有可命名的对象
	}
	defaultTitle := sessiondata.ClipTitle(firstUser)
	s.titled = true
	sess := &sessionAutoTitle{
		id:           s.id,
		defaultTitle: defaultTitle,
		firstUser:    firstUser,
		st:           s.st,
		stream:       s.stream,
		reg:          s.reg,
		emit:         s.emit,
		model:        s.model,
	}
	s.mu.Unlock()
	go sess.run()
}

// sessionAutoTitle 是命名调用的独立载荷（脱离会话锁运行——异步期间轮已结束，
// 会话可能继续被使用，不得持锁做网络 IO）。
type sessionAutoTitle struct {
	id           string
	defaultTitle string
	firstUser    string
	st           Persistence
	stream       StreamFn
	reg          *config.Registry
	emit  func(Event)
	model string
}

// run 执行命名：读库标题 → 竞争守卫 → 命名调用 → clip → Rename → 广播事件。
// 任何一步失败都静默返回（默认标题保留）。
func (t *sessionAutoTitle) run() {
	defer func() {
		// 命名是锦上添花，绝不让 panic 冒出去拖垮会话（emit 回调里有宿主代码）
		_ = recover()
	}()
	// 竞争守卫：用户此刻已手动改名（标题 ≠ 默认截断值）→ 放弃。
	cur, ok, err := t.st.TitleOf(t.id)
	if err != nil || !ok || (cur != "" && cur != t.defaultTitle) {
		return
	}
	// 模型解析（会话级选择优先，回落 default 角色——与 modelForRequest 同一
	// 优先级链，但命名不走 Agent 绑定：那是执行语境的绑定，不是会话的）。
	m, err := t.pickModel()
	if err != nil {
		return // 无可用模型——静默回落默认标题
	}
	ctx, cancel := context.WithTimeout(context.Background(), autoTitleTimeout)
	defer cancel()
	msgs := []llm.Message{
		{Role: "system", Content: autoTitleSystemPrompt},
		{Role: "user", Content: autoTitleUserPrefix + t.firstUser},
	}
	events, err := t.stream(ctx, m, msgs, []llm.Option{llm.WithMaxTokens(autoTitlePromptMaxTokens)})
	if err != nil {
		return
	}
	var title string
	for ev := range events {
		switch ev.Type {
		case llm.EventText:
			title += ev.TextDelta
		case llm.EventDone:
			if ev.Result != nil {
				title = ev.Result.Message.Content // 完整正文优先（非流式回放）
			}
		}
	}
	title = cleanTitle(title)
	if title == "" || title == t.defaultTitle {
		return // 空回复/没变化——保留默认标题
	}
	if err := t.st.Rename(t.id, title); err != nil {
		log.Printf("AI 自动命名落库失败（保留默认标题）: %v", err)
		return
	}
	t.emit(TitleRenamedEvent{ID: t.id, Title: title})
}

// pickModel 解析命名用的模型：会话级选择优先，回落 default 角色。
func (t *sessionAutoTitle) pickModel() (config.ModelConfig, error) {
	if t.model != "" {
		if m, ok := t.reg.Resolve(t.model); ok && m.Enabled {
			return m, nil
		}
	}
	return t.reg.ModelForRole(config.RoleDefault)
}

// cleanTitle 清洗模型回复：去引号/句号/空白/解释性前后缀，截回标题形态。
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"“”'「」『』《》")
	// 去掉常见的解释性包装（「标题：xxx」）
	if i := strings.Index(s, "："); i > 0 && i <= 6 {
		s = s[i+len("："):]
	}
	// 取第一行（模型偶尔会换行解释）
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	// 再截回 40 字形态（与默认标题同口径——侧栏一行以内）
	return sessiondata.ClipTitle(s)
}
