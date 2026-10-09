// 合并进程：后台任务（jobs）的**第二个 producer**（Kind="merge"）。
//
// 用户拍板的设计链：每轮对话结束一个检查点提交（上一批已完成），接着起合并进程
// ——用户视角始终是「在操作一个项目」，内部是「主检出 + 每会话一个分支/备份」。
//
// 任务体是**内置「合并 Agent」的独立子会话**（在集成分支的专用工作树里把本会话
// 分支的改动汇总进去）。三条纪律：
//  1. 合并只在**集成分支的工作树**里做——绝不在主检出里 merge，也不 push；
//  2. 失败/冲突如实 Settle(Failed, detail)——detail 写清冲突文件清单或失败原因，
//     绝不谎报成功；
//  3. 任务结束经**现有唤醒投递**（server/jobs.go 的 onJobEvent → deliverJobNotice）
//     自动通告回父会话——这里不写任何通知逻辑。
//
// EndedBy 取 EndedSelf（producer 自己收尾，与 bash 的 settleBackground 同口径）：
// 唤醒投递按 EndedBy 决定投不投（jobNoticeText）——EndedAgent 是「agent 调 job_kill」
// 的语义，投递会**被拦下**（同一轮内已知，投递只是噪声），那样父会话永远收不到合并
// 结束通告（验收用例 4 要的就是这条通告）。
package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/store"
)

const (
	// mergeJobKind 是合并任务的任务类型（jobs.Spec.Kind）。
	mergeJobKind = "merge"
	// defaultIntegrationBranch 是合并的默认目标分支。
	defaultIntegrationBranch = "lxcode/integration"
	// mergeIntegrationDir 是集成分支工作树在项目目录下的目录名
	//（路径 <sessions>/worktrees/<project-id>/integration）。
	mergeIntegrationDir = "integration"
	// mergerAgentID 是内置合并 Agent 的名单 id（任务体的执行者）。
	mergerAgentID = "merger"
	// mergeDetailMax 是任务 detail 的字符上限（冲突文件多时截断，不淹没通告）。
	mergeDetailMax = 600
)

// startMergeJob 起一个后台合并进程并立刻返回任务 id（producer 的第二处生产调用，
// 照 tools/bash.go 的 startBackground 写法）。
//
// pushAfter：合并成功后是否顺带把目标分支推到远程 origin（workspace_sync 的
// 「提交 → 合并 → push」链路；push 是**异步**做的——合并任务收尾前执行，失败
// 只把任务置为失败并写清「合并已完成，仅推送失败」，绝不误导为合并失败）。
//
// 前置校验（都在返回前做完，失败直接回给模型）：
//   - 会话存在且是项目会话（有 workspace）；
//   - 会话已有可合并的分支（worktree 元数据 Path/Branch 非空）；
//   - 同一会话没有在跑的合并任务（同一会话同时只允许一个）。
func (s *Server) startMergeJob(sessionID, targetBranch string, pushAfter bool) (string, error) {
	if s.st == nil {
		return "", fmt.Errorf("会话存储未启用，无法起合并进程")
	}
	mgr := s.jobsManager()
	if mgr == nil {
		return "", fmt.Errorf("后台任务未装配（后端未初始化任务管理器）")
	}
	if strings.TrimSpace(targetBranch) == "" {
		targetBranch = defaultIntegrationBranch
	}
	// 未分组会话（没有 worktree）：没有可合并的分支
	workspace, err := s.st.WorkspaceOf(sessionID)
	if err != nil {
		return "", err
	}
	if workspace == "" {
		return "", fmt.Errorf("未分组会话没有可合并的分支（先把它归入一个项目）")
	}
	meta, err := s.st.WorktreeOf(sessionID)
	if err != nil {
		return "", err
	}
	if meta.Path == "" || meta.Branch == "" {
		return "", fmt.Errorf("本会话还没有可合并的分支（先发一条消息建立工作树）")
	}
	projectMeta, found, err := s.st.ProjectByID(workspace)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("项目 %s 不存在", workspace)
	}
	// 同一会话同时只允许一个在跑的合并进程（重复调用允许——用户可以再起一次，
	// 但要等上一个收尾）
	for _, snap := range mgr.List(sessionID) {
		if snap.Kind == mergeJobKind && !snap.Status.Terminal() {
			return "", fmt.Errorf("本会话已有合并进程在跑（任务 %s）", snap.ID)
		}
	}
	title, err := s.st.TitleOf(sessionID)
	if err != nil {
		// 标题读不到不该挡住合并——退回一个中性标签
		title = ""
	}
	label := "合并 " + title
	if strings.TrimSpace(title) == "" {
		label = "合并会话"
	}
	j, err := mgr.Start(jobs.Spec{
		Kind: mergeJobKind, Label: label,
		SessionID: sessionID, OwnerSessionID: sessionID,
	})
	if err != nil {
		return "", fmt.Errorf("启动合并进程失败: %w", err)
	}
	// 拉子会话的父会话：用**请求方会话自己**（继承它的 store/stream/名单解析器），
	// 但子会话在集成分支的工作树里工作（RunAgentTask 的 workDir 覆盖）。
	sess, err := s.session(sessionID)
	if err != nil {
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "取会话运行时失败: "+mergeClip(err.Error()))
		return j.ID(), nil
	}
	go s.runMergeJob(j, sess, projectMeta.Path, workspace, meta, targetBranch, pushAfter)
	return j.ID(), nil
}

// runMergeJob 是合并任务的任务体（在 goroutine 里跑）：准备集成分支工作树 → 跑合并
// Agent 子会话 → 把结论写进 job → 按结果 Settle →（pushAfter 时）推送到 origin。
func (s *Server) runMergeJob(j jobs.Job, parent *agent.Session, projectPath, projectID string, meta store.WorktreeMeta, targetBranch string, pushAfter bool) {
	ctx, cancel := context.WithCancel(s.Ctx())
	defer cancel()
	// 取消登记：用户/agent 停任务时取消 ctx——子会话经 SendWait 的 ctx 传播中断并
	// **等它真正收尾**（对齐 docs/jobs.md §8「取消必须杀整棵进程树」的纪律）。
	if cr, ok := j.(jobs.CancelRegistrar); ok {
		cr.SetCancel(cancel)
	}
	integration, err := project.EnsureIntegrationWorktree(
		ctx, projectPath, s.st.WorktreeRoot(), projectID, mergeIntegrationDir, targetBranch)
	if err != nil {
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "准备集成分支工作树失败: "+mergeClip(err.Error()))
		return
	}
	fmt.Fprintf(j, "源分支: %s\n目标分支: %s\n集成工作树: %s\n\n",
		meta.Branch, targetBranch, integration.Path)
	conclusion, err := parent.RunAgentTask(ctx, mergerAgentID,
		mergeTaskText(meta.Branch, targetBranch, integration.Path), integration.Path, j)
	if err != nil {
		if ctx.Err() != nil {
			// 取消：沿用 Manager 记下的结束方（user/agent/backend），只有确实是自己
			// 退出才回落 self——与 bash 的 settleBackground 同一口径
			j.Settle(jobs.StatusKilled, mergeEndedBy(j), "已取消")
			return
		}
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "合并失败: "+mergeClip(err.Error()))
		return
	}
	if strings.TrimSpace(conclusion) != "" {
		fmt.Fprintf(j, "\n%s\n", conclusion)
	}
	// 冲突未解决 / 合并未完成 → 失败（detail 写清冲突文件清单）——绝不谎报成功
	files, inProgress, cerr := project.MergeConflicts(ctx, integration.Path)
	if cerr != nil {
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "检查合并结果失败: "+mergeClip(cerr.Error()))
		return
	}
	if inProgress || len(files) > 0 {
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, mergeConflictDetail(files, inProgress))
		return
	}
	// pushAfter：合并成功后把目标分支推到 origin（workspace_sync 的链路第三步）。
	// 措辞纪律：push 失败**不是合并失败**——detail 必须写清「合并已完成，仅推送失败」，
	// 免得父会话把已合并成功的改动当成没合（那是误导，会诱发重复合并）。
	if pushAfter {
		if hasRemote, rerr := project.RemoteExists(ctx, integration.Path, "origin"); rerr == nil && !hasRemote {
			j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "合并已完成；没有配置远程 origin，未推送")
			return
		}
		if err := project.PushBranch(ctx, integration.Path, targetBranch); err != nil {
			j.Settle(jobs.StatusFailed, jobs.EndedSelf, "合并已完成，仅推送失败: "+mergeClip(err.Error()))
			return
		}
		j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "合并完成，已推送 origin/"+targetBranch)
		return
	}
	j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "合并完成")
}

// mergeEndedBy 取任务当前的结束方（Kill 时 Manager 已记下）；空 = 回落 self。
func mergeEndedBy(j jobs.Job) jobs.EndedBy {
	if by := j.Snapshot().EndedBy; by != "" {
		return by
	}
	return jobs.EndedSelf
}

// mergeTaskText 组给合并 Agent 的任务说明书（自包含：分支、路径、纪律与验收标准）。
func mergeTaskText(sourceBranch, targetBranch, integrationPath string) string {
	return fmt.Sprintf(`把源会话分支 %s 的改动合并到目标分支 %s。

工作位置（就在这里工作，不要动别处）：%s
- 你已经在目标分支 %s 的专用工作树里；**绝不在主检出里 merge，也不 push**
- 源分支: %s（本会话分支，每轮结束有检查点提交）

合并纪律：
1. 先看两边改了什么（git log / git diff 源分支与目标分支），再动手合并——不盲目 merge
2. 冲突必须逐个解决并说明取舍：为什么保留这一边，另一边的意图如何被满足
3. **绝不用 --force、-X theirs / -X ours 掩盖冲突**——那是把别人的改动悄悄丢掉
4. 合并后跑构建与测试（用 bash），按仓库的验收标准验证
5. 失败就把目标分支恢复原状（git merge --abort 或回到合并前的提交）并**如实报告**，绝不谎报成功
6. 不丢弃任何人的改动：源分支与目标分支上的提交都不许改写

最后给出结论：合并是否成功、冲突文件与取舍、构建测试结果。`,
		sourceBranch, targetBranch, integrationPath, targetBranch, sourceBranch)
}

// mergeConflictDetail 组冲突失败的 detail（冲突文件清单优先，其次说明合并未完成）。
func mergeConflictDetail(files []string, inProgress bool) string {
	if len(files) > 0 {
		return mergeClip("合并未完成，仍有未解决的冲突: " + strings.Join(files, ", "))
	}
	if inProgress {
		return "合并未完成（还有未收尾的合并）"
	}
	return "合并未完成"
}

// mergeClip 截断任务 detail（通告要一眼看出原因，超长反而看不清）。
func mergeClip(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= mergeDetailMax {
		return string(r)
	}
	return string(r[:mergeDetailMax]) + "…"
}
