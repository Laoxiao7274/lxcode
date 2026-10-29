package store

// LastUserTurn 返回会话的轮次序号与最近一条真实用户消息正文（供轮结束的检查点
// 提交组装提交信息）。
//
// 轮次 = 真实用户消息数（与 foldSessionStats 的 turns 同口径：注入的提示条 notice
// 与压缩检查点 checkpoint 都不算）。钩子在**本轮用户消息已落库之后**触发，所以这个
// 计数就是从 1 开始的轮次序号——复用同一口径，不新增表列、也不另设自增计数器。
//
// **按 leaf 路径数**（2026-12 分支化）：切到旧分支后，全表计数会把废弃分支里的用户
// 消息算进来（提交信息取到一条当前历史里根本不存在的句子——静默的错数据）。
// 路径是「用户此刻看到的历史」，轮次与最近一句都必须与它同口径。
//
// 只读两件事：leaf 指针 + 路径上的行（走 readPathRows 的同一份回溯，不写第二套），
// 再套 surfaceRows（与回放同一份存活判定——被影子行不是历史）。
func (s *Store) LastUserTurn(id string) (turn int, content string, err error) {
	leaf, err := s.LeafSeq(id)
	if err != nil {
		return 0, "", err
	}
	rows, err := s.readPathRows(id, int(leaf))
	if err != nil {
		return 0, "", err
	}
	for _, r := range surfaceRows(rows) {
		if r.checkpoint || r.notice || r.msg.Role != "user" {
			continue
		}
		turn++
		content = r.msg.Content
	}
	return turn, content, nil
}
