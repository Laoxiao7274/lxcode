package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/moyunteng/lxcode/internal/llm"
)

// 调度工具的 id 改过名（历史名 → 现行名）。为什么必须迁移：
//
// OpenAI 与 Anthropic 都把工具名约束为 `^[a-zA-Z0-9_-]{1,64}$`，而旧名
// `agent.dispatch` 带点号——宽松网关过去放过，严格化的网关会 400 拒收**整轮**
// （2026-09-23 实测：主 Agent 每一轮都失败，改成下划线即通过；见 AGENTS.md §5 坑 13）。
// 代码里的 id 一改，库里两处引用就成了悬空：
//   - **白名单**（agents.tools）还指着旧名字 → 调度工具"不在白名单"，派发直接不可用；
//   - **历史**（messages.tool_calls）还写着旧名字 → 模型看到两个名字，且严格网关
//     可能连历史里的 tool_use 名一起校验。
//
// 这是**唯一一处刻意改写用户数据**的地方（种子同步一直避免碰 custom=1 行）：
// 它改的是标识符本身，不是语义。用 JSON 层改写而不是字符串替换——正文里完全可能
// 恰好出现同名的文字（事故报告、需求描述就在我们自己的历史里），字符串替换会误伤。
const (
	oldDispatchToolID = "agent.dispatch" // 历史名（带点号，违反工具名字符集）
	newDispatchToolID = "agent_dispatch" // 现行名（= tools.DispatchToolName；由迁移测试钉住二者一致）
)

// migrateDispatchToolID 把库里旧名字的调度工具 id 改成现行名（幂等：没有旧名字
// 的行不写库）。Open 时跑，与种子同步同一条路径——用户不需要做任何事。
func (s *Store) migrateDispatchToolID() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开改名迁移事务失败: %w", err)
	}
	defer tx.Rollback() // 已提交时是 no-op

	toolsN, err := migrateAgentToolLists(tx)
	if err != nil {
		return err
	}
	msgsN, err := migrateHistoryToolNames(tx)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交改名迁移失败: %w", err)
	}
	if toolsN > 0 || msgsN > 0 {
		log.Printf("调度工具 id 改名迁移完成：白名单 %d 行、历史 %d 条（%s → %s）",
			toolsN, msgsN, oldDispatchToolID, newDispatchToolID)
	}
	return nil
}

// migrateAgentToolLists 改白名单（agents.tools）。先把命中行读完再写——同一条
// 连接上边遍历结果集边写会互相挡（SQLite 单写者）。
func migrateAgentToolLists(tx *sql.Tx) (int, error) {
	type hit struct {
		id  string
		raw string
	}
	var hits []hit
	rows, err := tx.Query(`SELECT id, tools FROM agents WHERE tools LIKE ?`, "%"+oldDispatchToolID+"%")
	if err != nil {
		return 0, fmt.Errorf("查白名单失败: %w", err)
	}
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.raw); err != nil {
			rows.Close()
			return 0, fmt.Errorf("读白名单失败: %w", err)
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("遍历白名单失败: %w", err)
	}

	n := 0
	for _, h := range hits {
		var list []string
		if err := json.Unmarshal([]byte(h.raw), &list); err != nil {
			// 畸形行不碰（与配对不变量的纪律一致：畸形的既成事实不该让启动失败）
			log.Printf("白名单不是合法 JSON 数组，改名迁移跳过 %s: %v", h.id, err)
			continue
		}
		changed := false
		for i := range list {
			if list[i] == oldDispatchToolID {
				list[i] = newDispatchToolID
				changed = true
			}
		}
		if !changed {
			continue
		}
		enc, err := json.Marshal(list)
		if err != nil {
			return n, fmt.Errorf("重编码白名单失败（%s）: %w", h.id, err)
		}
		if _, err := tx.Exec(`UPDATE agents SET tools = ? WHERE id = ?`, string(enc), h.id); err != nil {
			return n, fmt.Errorf("更新白名单失败（%s）: %w", h.id, err)
		}
		n++
	}
	return n, nil
}

// migrateHistoryToolNames 改历史里工具调用的名字（messages.tool_calls 的
// function.name）。只动 name——参数、id、顺序一律不碰（配对与回放都按 id）。
func migrateHistoryToolNames(tx *sql.Tx) (int, error) {
	type hit struct {
		session string
		seq     int
		raw     string
	}
	var hits []hit
	rows, err := tx.Query(`SELECT session_id, seq, tool_calls FROM messages WHERE tool_calls LIKE ?`, "%"+oldDispatchToolID+"%")
	if err != nil {
		return 0, fmt.Errorf("查历史工具调用失败: %w", err)
	}
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.session, &h.seq, &h.raw); err != nil {
			rows.Close()
			return 0, fmt.Errorf("读历史工具调用失败: %w", err)
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("遍历历史工具调用失败: %w", err)
	}

	n := 0
	for _, h := range hits {
		var calls []llm.ToolCall
		if err := json.Unmarshal([]byte(h.raw), &calls); err != nil {
			log.Printf("历史工具调用不是合法 JSON（%s#%d），改名迁移跳过: %v", h.session, h.seq, err)
			continue
		}
		changed := false
		for i := range calls {
			if calls[i].Function.Name == oldDispatchToolID {
				calls[i].Function.Name = newDispatchToolID
				changed = true
			}
		}
		if !changed {
			continue
		}
		enc, err := json.Marshal(calls)
		if err != nil {
			return n, fmt.Errorf("重编码历史工具调用失败（%s#%d）: %w", h.session, h.seq, err)
		}
		if _, err := tx.Exec(`UPDATE messages SET tool_calls = ? WHERE session_id = ? AND seq = ?`,
			string(enc), h.session, h.seq); err != nil {
			return n, fmt.Errorf("更新历史工具调用失败（%s#%d）: %w", h.session, h.seq, err)
		}
		n++
	}
	return n, nil
}
