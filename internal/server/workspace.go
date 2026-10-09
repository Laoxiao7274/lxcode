// workspace_status / workspace_sync / workspace_rollback 三个 Agent 工具的
// server 侧实现：「用户无感」链路的最后一块——用户视角始终是「在操作一个项目」，
// 内部是「主检出 + 每会话一个分支/备份 + 集成分支」。
//
// 三条纪律（对齐既有 mergejob / autocommit 的口径）：
//  1. status **只读**——全部走 project 包带 ctx+超时的只读封装，绝不动工作区；
//  2. sync 的 push 是**合并任务的收尾步骤**（runMergeJob 里做），本文件只负责
//     把 pushAfter 传进去——push 失败不影响已完成的合并；
//  3. rollback **只作用于本会话分支**，绝不 push、绝不动集成分支与主检出；
//     脏工作区一律拒绝（绝不静默丢弃用户手工改动）。
package server

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/store"
)

// statusFileLimit 是状态清单的展示上限（条）：清单是给模型看的汇报素材，
// 上百条未跟踪文件会把上下文灌满——截断并注明总数。
const statusFileLimit = 40

// syncCheckpointLimit 等文本清单的通用截断（回滚丢弃文件清单同款）。
const syncFileLimit = 40

// workspaceStore 返回会话存储；未启用时报自解释错误。
func (s *Server) workspaceStore() (*store.Store, error) {
	if s.st == nil {
		return nil, fmt.Errorf("会话存储未启用，无法执行工作区操作")
	}
	return s.st, nil
}

// resolveProject 解析会话归属的项目（projectID 非空时优先用它，供跨会话查询）。
// 未分组会话返回自解释错误——三个工具共同的第一个门槛。
func (s *Server) resolveProject(sessionID, projectID string) (store.ProjectMeta, error) {
	st, err := s.workspaceStore()
	if err != nil {
		return store.ProjectMeta{}, err
	}
	ws := strings.TrimSpace(projectID)
	if ws == "" {
		ws, err = st.WorkspaceOf(sessionID)
		if err != nil {
			return store.ProjectMeta{}, err
		}
	}
	if ws == "" {
		return store.ProjectMeta{}, fmt.Errorf("当前会话没有归属项目（先把会话归入一个项目，工作区操作才有对象）")
	}
	meta, found, err := st.ProjectByID(ws)
	if err != nil {
		return store.ProjectMeta{}, err
	}
	if !found {
		return store.ProjectMeta{}, fmt.Errorf("项目 %s 不存在", ws)
	}
	return meta, nil
}

// clipLines 把清单截到 limit 条并注明总数（空清单返回空串）。
func clipLines(lines []string, limit int) string {
	if len(lines) == 0 {
		return ""
	}
	shown := lines
	note := ""
	if len(lines) > limit {
		shown = lines[:limit]
		note = fmt.Sprintf("\n…（共 %d 条，只列前 %d 条）", len(lines), limit)
	}
	return strings.Join(shown, "\n") + note
}

// workspaceStatus 汇报项目工作区状态：主检出 + 各会话分支 + 集成分支（全只读）。
func (s *Server) workspaceStatus(sessionID, projectID string) (string, error) {
	meta, err := s.resolveProject(sessionID, projectID)
	if err != nil {
		return "", err
	}
	ctx := s.Ctx()
	main, err := project.ProjectStatus(ctx, meta.Path)
	if err != nil {
		return "", fmt.Errorf("读取主检出状态失败: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "项目 %s（%s）\n\n【主检出】\n- 路径：%s\n- 当前分支：%s\n", meta.Name, meta.ID, meta.Path, main.Branch)
	if len(main.Files) == 0 {
		b.WriteString("- 未提交改动：无（干净）\n")
	} else {
		fmt.Fprintf(&b, "- 未提交/未跟踪（%d 条）：\n%s\n", len(main.Files), clipLines(main.Files, statusFileLimit))
	}

	// 各会话分支：按项目过滤（含归档——归档的分支同样是备份，如实标注）。
	st, err := s.workspaceStore()
	if err != nil {
		return "", err
	}
	sessions, err := st.List()
	if err != nil {
		return "", fmt.Errorf("列出会话失败: %w", err)
	}
	b.WriteString("\n【会话分支】\n")
	listed := false
	for _, sess := range sessions {
		if sess.Workspace != meta.ID {
			continue
		}
		listed = true
		wt, err := st.WorktreeOf(sess.ID)
		if err != nil {
			fmt.Fprintf(&b, "- %s（%s）：读取 worktree 元数据失败: %v\n", sess.Title, sess.ID, err)
			continue
		}
		if wt.Branch == "" {
			fmt.Fprintf(&b, "- %s（%s）：还没有会话分支（未发过消息）%s\n", sess.Title, sess.ID, archivedNote(sess.Archived))
			continue
		}
		// ahead：base 用会话自己的基线提交；拿不到就以主检出当前分支为参考点并注明。
		base, refNote := wt.BaseCommit, ""
		if base == "" {
			base, refNote = main.Branch, "（参考点为主检出当前分支）"
		}
		ahead, err := project.CommitCountBetween(ctx, meta.Path, base, wt.Branch)
		if err != nil {
			fmt.Fprintf(&b, "- %s（%s，分支 %s%s）：统计提交失败: %v\n", sess.Title, sess.ID, wt.Branch, archivedNote(sess.Archived), err)
			continue
		}
		line := fmt.Sprintf("- %s（%s，分支 %s%s）：领先参考点 %d 个提交", sess.Title, sess.ID, wt.Branch, archivedNote(sess.Archived), ahead)
		if refNote != "" {
			line += refNote
		}
		if wt.Path != "" {
			if _, statErr := os.Stat(wt.Path); statErr == nil {
				dirty, err := project.StatusLines(ctx, wt.Path)
				if err != nil {
					line += fmt.Sprintf("；有工作树（改动查询失败: %v）", err)
				} else if len(dirty) == 0 {
					line += "；有工作树（干净）"
				} else {
					line += fmt.Sprintf("；有工作树（%d 条未提交改动）", len(dirty))
				}
			} else {
				// 「分支存在但目录不在」是常态（释放只删目录、保留分支与元数据）
				line += "；无工作树（分支保留，可恢复）"
			}
		} else {
			line += "；无工作树（未创建或已释放）"
		}
		if merged, err := project.IsAncestor(ctx, meta.Path, wt.Branch, main.Branch); err != nil {
			line += fmt.Sprintf("；合并状态查询失败: %v", err)
		} else if merged {
			line += "；已并入主检出当前分支"
		}
		b.WriteString(line + "\n")
	}
	if !listed {
		b.WriteString("- （该项目下还没有会话分支）\n")
	}

	// 集成分支（若存在）：ahead/behind 相对主检出当前分支。
	const integration = "lxcode/integration"
	exists, err := project.BranchExists(ctx, meta.Path, integration)
	if err != nil {
		return "", fmt.Errorf("查询集成分支失败: %w", err)
	}
	b.WriteString("\n【集成分支】\n")
	if !exists {
		b.WriteString("- lxcode/integration 尚未创建（还没有合并过）\n")
	} else {
		ahead, err := project.CommitCountBetween(ctx, meta.Path, main.Branch, integration)
		if err != nil {
			return "", fmt.Errorf("统计集成分支提交失败: %w", err)
		}
		behind, err := project.CommitCountBetween(ctx, meta.Path, integration, main.Branch)
		if err != nil {
			return "", fmt.Errorf("统计主检出提交失败: %w", err)
		}
		fmt.Fprintf(&b, "- lxcode/integration：领先主检出 %d 个提交，落后 %d 个提交\n", ahead, behind)
	}
	return b.String(), nil
}

// archivedNote 归档标注（清单里如实区分，不隐藏）。
func archivedNote(archived bool) string {
	if archived {
		return "，已归档"
	}
	return ""
}

// workspaceSync 执行「提交 → 起合并进程（→ 合并成功后 push）」链路：
// 前两步同步做，push 异步在合并任务收尾时做（见 mergejob.go 的 pushAfter）。
func (s *Server) workspaceSync(sessionID, message string, push bool) (string, error) {
	st, err := s.workspaceStore()
	if err != nil {
		return "", err
	}
	ws, err := st.WorkspaceOf(sessionID)
	if err != nil {
		return "", err
	}
	if ws == "" {
		return "", fmt.Errorf("当前会话没有归属项目，无法提交推送（先把会话归入一个项目）")
	}
	wt, err := st.WorktreeOf(sessionID)
	if err != nil {
		return "", err
	}
	if wt.Path == "" || wt.Branch == "" {
		return "", fmt.Errorf("本会话还没有工作树（先发一条消息建立），没有可提交的内容")
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		// 默认取最近一条用户消息的首行（与自动提交的摘要同一套截断）。
		if _, lastUser, err := st.LastUserTurn(sessionID); err == nil {
			msg = commitSummary(lastUser)
		}
		if msg == "" {
			msg = "工作区同步"
		}
	}
	// 第一步：提交本会话 worktree（干净则跳过，不报错）。
	committed, err := project.CommitAll(s.Ctx(), wt.Path, msg)
	if err != nil {
		return "", fmt.Errorf("提交失败: %w", err)
	}
	// 第二步：起合并进程（同一条路径与前置校验——含「同一会话只允许一个」）。
	jobID, err := s.startMergeJob(sessionID, "", push)
	if err != nil {
		if committed {
			return "", fmt.Errorf("已提交检查点（%s），但起合并进程失败: %w", msg, err)
		}
		return "", err
	}
	var b strings.Builder
	if committed {
		// 检查点序号 = 相对基线的提交数（每次提交恰好 +1）。
		n := 0
		base := wt.BaseCommit
		if base == "" {
			base = "HEAD^"
		}
		if cnt, cerr := project.CommitCountBetween(s.Ctx(), wt.Path, base, "HEAD"); cerr == nil {
			n = cnt
		}
		fmt.Fprintf(&b, "已提交第 %d 个检查点（%s）。", n, msg)
	} else {
		b.WriteString("工作区没有未提交改动，跳过提交。")
	}
	fmt.Fprintf(&b, "合并进程已启动（任务 %s），结束后会自动通知你。", jobID)
	if push {
		b.WriteString("推送将在合并成功后执行（推到远程 origin）。")
	} else {
		b.WriteString("本次不推送。")
	}
	return b.String(), nil
}

// rollbackPlan 是回滚的执行计划（确认门在计划上做，执行只照计划做）。
type rollbackPlan struct {
	worktree string   // 本会话工作树路径
	oldHead  string   // 回滚前的 HEAD
	newHead  string   // 回滚到的提交
	files    []string // 将被丢弃的文件清单（git diff --name-status 新..旧）
	warns    []string // 警告（如「该提交已合并进集成分支」）
	drops    int      // 将丢弃的检查点提交数
}

// planRollback 生成回滚计划并做全部前置校验（只读，不动工作区）：
//   - 脏工作区拒绝（绝不静默丢弃用户手工改动）；
//   - target 是 hash 时校验它在本会话分支上；
//   - 目标提交已并入集成分支时生成警告（回滚不撤销集成分支上的内容）。
func (s *Server) planRollback(sessionID, target string) (*rollbackPlan, error) {
	st, err := s.workspaceStore()
	if err != nil {
		return nil, err
	}
	ws, err := st.WorkspaceOf(sessionID)
	if err != nil {
		return nil, err
	}
	if ws == "" {
		return nil, fmt.Errorf("当前会话没有归属项目，没有可回滚的会话分支")
	}
	wt, err := st.WorktreeOf(sessionID)
	if err != nil {
		return nil, err
	}
	if wt.Path == "" || wt.Branch == "" {
		return nil, fmt.Errorf("本会话还没有工作树（先发一条消息建立），没有可回滚的分支")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		return nil, fmt.Errorf("本会话工作树目录不存在（可能已被释放），无法回滚")
	}
	ctx := s.Ctx()
	oldHead, err := project.ResolveCommit(ctx, wt.Path, "HEAD")
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(target) {
	case "", "last-turn":
		// HEAD 就是基线 = 还没有任何轮次提交。
		if wt.BaseCommit != "" && oldHead == wt.BaseCommit {
			return nil, fmt.Errorf("本会话还没有可回滚的提交（当前就在会话基线上）")
		}
		head, err := project.ResolveCommit(ctx, wt.Path, "HEAD~1")
		if err != nil {
			return nil, fmt.Errorf("本会话还没有可回滚的提交: %w", err)
		}
		return s.finishRollbackPlan(ctx, wt, oldHead, head)
	case "session-start":
		base := wt.BaseCommit
		if base == "" {
			// 基线元数据缺失：退回根提交（仓库最早的提交即会话可回的起点）。
			roots, err := project.RootCommits(ctx, wt.Path)
			if err != nil || len(roots) == 0 {
				return nil, fmt.Errorf("会话基线未知且找不到根提交，无法回滚到会话起点")
			}
			base = roots[len(roots)-1]
		}
		return s.finishRollbackPlan(ctx, wt, oldHead, base)
	default:
		// 原始 commit hash：必须在本会话分支上（是 HEAD 的祖先）才允许。
		head, err := project.ResolveCommit(ctx, wt.Path, strings.TrimSpace(target))
		if err != nil {
			return nil, err
		}
		ok, err := project.IsAncestor(ctx, wt.Path, head, oldHead)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("提交 %s 不在本会话分支上，拒绝回滚（只允许回滚本会话分支自己的历史）", head)
		}
		return s.finishRollbackPlan(ctx, wt, oldHead, head)
	}
}

// finishRollbackPlan 在目标提交定下来之后补齐计划的公共部分：
// 脏工作区拒绝、丢弃文件清单、丢弃提交数、集成分支警告。
func (s *Server) finishRollbackPlan(ctx context.Context, wt store.WorktreeMeta, oldHead, newHead string) (*rollbackPlan, error) {
	// 硬纪律：reset 前工作区必须干净——用户手工改动绝不被静默丢弃。
	dirty, err := project.StatusLines(ctx, wt.Path)
	if err != nil {
		return nil, fmt.Errorf("检查工作区状态失败: %w", err)
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("工作区有未提交改动，先提交或说明如何处理：\n%s", clipLines(dirty, 10))
	}
	plan := &rollbackPlan{worktree: wt.Path, oldHead: oldHead, newHead: newHead}
	// 丢弃的文件清单：方向是 新..旧——状态字母表示「回滚会丢掉什么」（A=将删除）。
	files, err := project.DiffNameStatus(ctx, wt.Path, newHead, oldHead)
	if err != nil {
		return nil, fmt.Errorf("统计将被丢弃的文件失败: %w", err)
	}
	plan.files = files
	if drops, err := project.CommitCountBetween(ctx, wt.Path, newHead, oldHead); err == nil {
		plan.drops = drops
	}
	// 集成分支警告：目标提交已经并进集成分支的话，本会话分支的回滚不会撤销
	// 集成分支上的内容——必须如实警告，不能让用户以为撤销生效了。
	const integration = "lxcode/integration"
	if exists, err := project.BranchExists(ctx, wt.Path, integration); err == nil && exists {
		if merged, err := project.IsAncestor(ctx, wt.Path, newHead, integration); err == nil && merged {
			plan.warns = append(plan.warns,
				"该提交已合并进集成分支，本会话分支的回滚不会撤销集成分支上的内容；如需撤销请让我在集成分支上 revert。")
		}
	}
	return plan, nil
}

// rollbackConfirmText 组确认门文案（「将丢弃第 N 个检查点，涉及哪些文件」）。
// 计划生成失败时回一条通用文案——确认门照常生效，具体错误在执行时报给模型。
func (s *Server) rollbackConfirmText(sessionID, target string) string {
	label := strings.TrimSpace(target)
	if label == "" {
		label = "last-turn"
	}
	plan, err := s.planRollback(sessionID, target)
	if err != nil {
		return fmt.Sprintf("将把本会话分支回滚（目标：%s），丢弃其后的检查点提交。该操作不可逆，确认执行？", label)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "将把本会话分支从 %s 回滚到 %s，丢弃其后的 %d 个检查点提交（该操作不可逆）。",
		short7(plan.oldHead), short7(plan.newHead), plan.drops)
	if len(plan.files) > 0 {
		fmt.Fprintf(&b, "\n涉及的文件：\n%s", clipLines(plan.files, syncFileLimit))
	} else {
		b.WriteString("\n不涉及文件内容变化（只移动分支指针）。")
	}
	for _, w := range plan.warns {
		b.WriteString("\n警告：" + w)
	}
	return b.String()
}

// syncConfirmText 组 sync 的确认门文案（说清将执行「提交 + 合并 +（可选）推送」
// 哪些动作——用户必须知道自己确认的是什么）。
func (s *Server) syncConfirmText(sessionID string, push bool) string {
	var b strings.Builder
	b.WriteString("将执行：\n1. 提交本会话工作区的全部改动（git add -A + commit）")
	if _, err := s.workspaceStore(); err == nil {
		if _, lastUser, uerr := s.st.LastUserTurn(sessionID); uerr == nil {
			if msg := commitSummary(lastUser); msg != "" {
				fmt.Fprintf(&b, "，提交信息：%s", msg)
			}
		}
	}
	b.WriteString("\n2. 起合并进程，把改动合并到集成分支（后台任务，结束后自动通知）")
	if push {
		b.WriteString("\n3. 合并成功后推送到远程 origin")
	}
	b.WriteString("\n确认执行？")
	return b.String()
}

// workspaceRollback 执行回滚（计划在前置校验阶段生成，这里照计划 reset）。
func (s *Server) workspaceRollback(sessionID, target string) (string, error) {
	plan, err := s.planRollback(sessionID, target)
	if err != nil {
		return "", err
	}
	if err := project.ResetHard(s.Ctx(), plan.worktree, plan.newHead); err != nil {
		return "", fmt.Errorf("回滚失败: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "已回滚到提交 %s（丢弃了其后的 %d 个检查点提交）。", short7(plan.newHead), plan.drops)
	if len(plan.files) == 0 {
		b.WriteString("\n丢弃的文件：无（只移动了分支指针）。")
	} else {
		fmt.Fprintf(&b, "\n丢弃的文件：\n%s", clipLines(plan.files, syncFileLimit))
	}
	for _, w := range plan.warns {
		b.WriteString("\n警告：" + w)
	}
	return b.String(), nil
}

// short7 取 hash 前 7 位（展示用；空串安全）。
func short7(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}
