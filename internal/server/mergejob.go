// 合并进程：后台任务（jobs）的**第二个 producer**（Kind="merge"）。
//
// 用户拍板的设计链：每轮对话结束一个检查点提交（上一批已完成），接着起合并进程
// ——用户视角始终是「在操作一个项目」，内部是「主检出 + 每会话一个分支/备份」。
//
// 任务体是**内置「合并 Agent」的独立子会话**（在集成分支的专用工作树里把**本项目
// 全部有改动的会话分支**汇总进去）。四条纪律：
//  1. 合并只在**集成分支的工作树**里做——绝不在主检出里 merge，也不 push；
//  2. 范围硬限定本项目：发起时扫描项目下全部有改动的会话分支（ahead>0 或工作树脏），
//     任务说明书显式声明「不把清单之外的任何文件或提交带进目标分支」；
//  3. 失败/冲突/越界如实 Settle(Failed, detail)——detail 写清冲突文件清单、失败原因
//     或不可归因的提交清单，绝不谎报成功；发现越界提交还执行**硬回滚**
//     （集成分支 reset --hard 回任务开始前的 HEAD）；
//  4. 任务结束经**现有唤醒投递**（server/jobs.go 的 onJobEvent → deliverJobNotice）
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
	"os"
	"strings"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/project"
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
	// mergeMaxScanBranches 是单次合并扫描的分支上限：每个分支要跑几个 git 查询，
	// 分支太多既慢又会把任务说明书撑爆——超出直接拒绝，请分批合并。
	mergeMaxScanBranches = 50
)

// mergeSourceBranch 是扫描出的一个待合并源分支（任务说明书与收尾来源校验共用）。
type mergeSourceBranch struct {
	Branch    string // 会话分支名（lxcode/session-<id>）
	SessionID string
	Title     string // 会话标题（说明书里给人看的线索）
	Archived  bool   // 归档会话的分支同样是改动备份，如实标注
	Ahead     int    // 领先参考点的提交数（见 scanProjectBranches 的口径）
	Dirty     int    // 工作树未提交改动条数（目录不在时为 0）
}

// startMergeJob 起一个后台合并进程并立刻返回任务 id（producer 的第二处生产调用，
// 照 tools/bash.go 的 startBackground 写法）。
//
// pushAfter：合并成功后是否顺带把目标分支推到远程 origin（workspace_sync 的
// 「提交 → 合并 → push」链路；push 是**异步**做的——合并任务收尾前执行，失败
// 只把任务置为失败并写清「合并已完成，仅推送失败」，绝不误导为合并失败）。
func (s *Server) startMergeJob(sessionID, targetBranch string, pushAfter bool) (string, error) {
	return s.startMergeJobOpts(sessionID, targetBranch, pushAfter, false)
}

// startMergeJobOpts 是 startMergeJob 的本体，autoHook 区分两条调用路径：
//
//   - autoHook=false（工具路径，merge_request / workspace_sync）：扫描失败也要起
//     一条任务并让它以失败收尾——模型能经 job_output 读到失败原因（原有语义）；
//   - autoHook=true（轮末自动合并钩子，autocommit.go 的 maybeAutoMerge）：扫描发现
//     「全项目没有待合并的改动」时直接把原因交回钩子静默记日志，**不创建任务**
//     ——钩子不缺这份反馈（它只记日志），给用户 job 面板留一条失败任务纯是噪声。
//
// 前置校验（都在返回前做完，失败直接回给模型）：
//   - 会话存在且是项目会话（有 workspace）；
//   - 会话已有可合并的分支（worktree 元数据 Path/Branch 非空）；
//   - 同一**项目**没有在跑的合并任务（2026-10 升级：从「按会话」扩为「按项目」
//     ——同一项目多个会话各起合并进程会往同一集成分支互相踩）；
//   - 扫描出本项目至少一个有改动的会话分支（含发起会话自身）。
func (s *Server) startMergeJobOpts(sessionID, targetBranch string, pushAfter, autoHook bool) (string, error) {
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
	// 前置提交（见 autocommit.go 的 commitBeforeMerge）：merge_request 是模型轮内
	// 调用的工具，本轮改动还没被 TurnDone 的自动提交落盘——不先提交的话，下面的
	// 扫描把它们算成 dirty，而 merger 只合提交，本轮改动要等下次合并。工作树干净
	// 则直接继续（无空提交）；git 出错则不起任务（fail-closed）。
	if err := s.commitBeforeMerge(sessionID, meta.Path); err != nil {
		return "", err
	}
	projectMeta, found, err := s.st.ProjectByID(workspace)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("项目 %s 不存在", workspace)
	}
	// 标题先读好（label 要进 Spec）——读不到不挡合并，退回中性标签。
	title, err := s.st.TitleOf(sessionID)
	if err != nil {
		title = ""
	}
	label := "合并 " + title
	if strings.TrimSpace(title) == "" {
		label = "合并会话"
	}
	// 扫描本项目有改动的会话分支（含归档；含发起会话自身——扫描无偏心，
	// 它的分支按同一套标准判定，无改动则跳过并在说明书注明）。
	// 扫描挪到任务创建之前（2026-10，随自动合并钩子而来）：钩子路径遇到
	// 「全项目没有待合并的改动」不该给用户留一条失败任务——把原因交回钩子
	// 静默记日志即可；工具路径保持原语义（起一条任务、失败收尾，模型经
	// job_output 读得到原因）。
	sources, initiatorNote, err := s.scanProjectBranches(s.Ctx(), projectMeta.Path, workspace, targetBranch, sessionID)
	if err != nil {
		if autoHook && strings.Contains(err.Error(), "没有待合并的改动") {
			return "", err
		}
		return s.startFailedMergeJob(sessionID, label, workspace, err)
	}
	// 按项目互斥：扫描全部在跑任务（List("") 列全部），同项目已有未收尾的合并
	// 进程就拒绝。job 归属的项目记在 s.mergeJobProject（jobs.Spec 没有项目字段，
	// 这是改动最小的跟随方式）。检查与登记都持 mergeMu——否则两个并发发起会
	// 在「检查通过但还没登记」的窗口里互相放行。
	s.mergeMu.Lock()
	if s.mergeJobProject == nil {
		s.mergeJobProject = map[string]string{}
	}
	for _, snap := range mgr.List("") {
		if snap.Kind != mergeJobKind || snap.Status.Terminal() {
			continue
		}
		if s.mergeJobProject[snap.ID] == workspace {
			s.mergeMu.Unlock()
			return "", fmt.Errorf("该项目已有合并进程在跑（任务 %s）", snap.ID)
		}
	}
	// 先占位再释放锁：任务 id 在 Start 之后才知道，Start 也放进锁内保证
	// 「检查 → 登记」原子（Start 只拿 Manager 自己的锁，很快）。
	j, err := mgr.Start(jobs.Spec{
		Kind: mergeJobKind, Label: label,
		SessionID: sessionID, OwnerSessionID: sessionID,
	})
	if err != nil {
		s.mergeMu.Unlock()
		return "", fmt.Errorf("启动合并进程失败: %w", err)
	}
	s.mergeJobProject[j.ID()] = workspace
	s.mergeMu.Unlock()
	// 拉子会话的父会话：用**请求方会话自己**（继承它的 store/stream/名单解析器），
	// 但子会话在集成分支的工作树里工作（RunAgentTask 的 workDir 覆盖）。
	sess, err := s.session(sessionID)
	if err != nil {
		s.forgetMergeProject(j.ID())
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "取会话运行时失败: "+mergeClip(err.Error()))
		return j.ID(), nil
	}
	go s.runMergeJob(j, sess, projectMeta.Path, workspace, targetBranch, pushAfter, sources, initiatorNote)
	return j.ID(), nil
}

// startFailedMergeJob 起一条**立刻失败收尾**的合并任务（工具路径的扫描失败语义，
// 2026-10 起从 startMergeJobOpts 拆出——扫描挪到了任务创建之前）：模型拿到任务 id
// 后可经 job_output 读到失败原因，而不是收到一条裸错误字符串。按项目互斥照做
//（同项目已有在跑的合并进程就拒绝，与正常路径同一道闸）；任务转瞬即终态，
// 互斥登记随做随注销，不留项目占用。
func (s *Server) startFailedMergeJob(sessionID, label, workspace string, cause error) (string, error) {
	mgr := s.jobsManager()
	s.mergeMu.Lock()
	if s.mergeJobProject == nil {
		s.mergeJobProject = map[string]string{}
	}
	for _, snap := range mgr.List("") {
		if snap.Kind != mergeJobKind || snap.Status.Terminal() {
			continue
		}
		if s.mergeJobProject[snap.ID] == workspace {
			s.mergeMu.Unlock()
			return "", fmt.Errorf("该项目已有合并进程在跑（任务 %s）", snap.ID)
		}
	}
	j, err := mgr.Start(jobs.Spec{
		Kind: mergeJobKind, Label: label,
		SessionID: sessionID, OwnerSessionID: sessionID,
	})
	if err != nil {
		s.mergeMu.Unlock()
		return "", fmt.Errorf("启动合并进程失败: %w", err)
	}
	s.mergeJobProject[j.ID()] = workspace
	s.mergeMu.Unlock()
	s.forgetMergeProject(j.ID())
	j.Settle(jobs.StatusFailed, jobs.EndedSelf, mergeClip(cause.Error()))
	return j.ID(), nil
}

// scanProjectBranches 扫描项目下全部顶层会话（含归档）的分支，筛出「有改动」的：
//
//   - ahead 口径：参考点 = 集成分支（已存在时）——合进集成分支的内容不再算改动；
//     集成分支尚不存在时它将从主检出当前 HEAD 创建，参考点用主检出当前分支。
//     ahead = CommitCountBetween(参考点, 分支)（分支有而参考点没有的提交数）；
//   - dirty 口径：会话工作树目录存在时 status --porcelain --untracked-files=all
//     的行数（与 workspace_status 的「N 条未提交改动」同一口径）；目录不在
//     （已释放/未创建）时视为 0——分支上的提交已由 ahead 覆盖；
//   - 入选条件 = ahead > 0 || dirty > 0；已并入集成分支（IsAncestor）的分支
//     ahead 必为 0，连同干净工作树一起被跳过——合过的再合一遍是空转；
//   - 发起会话自身不豁免：无改动则不进清单，但**必须注明**（initiatorNote），
//     绝不静默消失——用户点的是「合并」，发起会话分支去哪了要说清；
//   - 全项目一个可合内容都没有（连发起会话也无）→ 报错，不起空转任务。
//
// 上限保护：入选分支超过 mergeMaxScanBranches 直接报错（每个分支几个 git 调用，
// 分支多时既慢又会把任务说明书撑爆）。
func (s *Server) scanProjectBranches(ctx context.Context, projectPath, projectID, targetBranch, initiatorID string) (sources []mergeSourceBranch, initiatorNote string, err error) {
	sessions, err := s.st.List()
	if err != nil {
		return nil, "", fmt.Errorf("列出会话失败: %w", err)
	}
	// 参考点：集成分支存在 → 相对集成分支；不存在 → 主检出当前分支
	//（EnsureIntegrationWorktree 会从主检出 HEAD 创建它）。
	ref := targetBranch
	integrationExists, err := project.BranchExists(ctx, projectPath, targetBranch)
	if err != nil {
		return nil, "", fmt.Errorf("查询集成分支失败: %w", err)
	}
	if !integrationExists {
		if ref, err = project.CurrentBranch(ctx, projectPath); err != nil {
			return nil, "", fmt.Errorf("读取主检出当前分支失败: %w", err)
		}
	}
	for _, sess := range sessions {
		if sess.Workspace != projectID {
			continue
		}
		wt, err := s.st.WorktreeOf(sess.ID)
		if err != nil || wt.Branch == "" {
			continue // 没 worktree 元数据 = 从未发过消息，没有分支可合
		}
		if exists, berr := project.BranchExists(ctx, projectPath, wt.Branch); berr != nil || !exists {
			continue // 分支引用不在（理论上不该发生），防御性跳过
		}
		// dirty 先算（目录存在时）：已并入集成分支但工作树有未提交改动的分支
		// 仍然有可合内容，不能因为「合过」就跳过。
		dirty := 0
		if wt.Path != "" {
			if _, serr := os.Stat(wt.Path); serr == nil {
				if lines, derr := project.StatusLines(ctx, wt.Path); derr == nil {
					dirty = len(lines)
				}
			}
		}
		if integrationExists {
			merged, merr := project.IsAncestor(ctx, projectPath, wt.Branch, targetBranch)
			if merr != nil {
				continue // 合并状态查不出来就不猜——跳过并让其他分支照常走
			}
			if merged && dirty == 0 {
				if sess.ID == initiatorID {
					initiatorNote = fmt.Sprintf("发起会话分支 %s 已并入集成分支且工作树无改动", wt.Branch)
				}
				continue
			}
		}
		ahead, aerr := project.CommitCountBetween(ctx, projectPath, ref, wt.Branch)
		if aerr != nil {
			continue // ahead 统计失败不猜（宁可漏一个分支也不编数字）
		}
		if ahead == 0 && dirty == 0 {
			if sess.ID == initiatorID {
				initiatorNote = fmt.Sprintf("发起会话分支 %s 无未合并改动（领先 0 个提交，工作树干净）", wt.Branch)
			}
			continue
		}
		if len(sources) >= mergeMaxScanBranches {
			return nil, "", fmt.Errorf("本项目待合并的会话分支超过 %d 个，请分批合并", mergeMaxScanBranches)
		}
		sources = append(sources, mergeSourceBranch{
			Branch: wt.Branch, SessionID: sess.ID, Title: sess.Title,
			Archived: sess.Archived, Ahead: ahead, Dirty: dirty,
		})
	}
	if len(sources) == 0 {
		if initiatorNote != "" {
			return nil, "", fmt.Errorf("本项目没有待合并的改动（%s）", initiatorNote)
		}
		return nil, "", fmt.Errorf("本项目没有待合并的改动")
	}
	return sources, initiatorNote, nil
}

// runMergeJob 是合并任务的任务体（在 goroutine 里跑）：准备集成分支工作树 → 跑合并
// Agent 子会话 → 把结论写进 job → 来源校验（越界即回滚）→ 按结果 Settle →
//（pushAfter 时）推送到 origin。
func (s *Server) runMergeJob(j jobs.Job, parent *agent.Session, projectPath, projectID string, targetBranch string, pushAfter bool, sources []mergeSourceBranch, initiatorNote string) {
	defer s.forgetMergeProject(j.ID()) // 按项目互斥的登记随任务收尾注销
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
	// 任务开始前集成分支的 HEAD：收尾来源校验的基线（越界即回滚到这里）。
	oldHead, err := project.ResolveCommit(ctx, integration.Path, "HEAD")
	if err != nil {
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "记录集成分支基线失败: "+mergeClip(err.Error()))
		return
	}
	fmt.Fprintf(j, "目标分支: %s\n集成工作树: %s\n源分支:\n", targetBranch, integration.Path)
	for _, src := range sources {
		fmt.Fprintf(j, "- %s（领先 %d 个提交）\n", src.Branch, src.Ahead)
	}
	if initiatorNote != "" {
		fmt.Fprintf(j, "（%s）\n", initiatorNote)
	}
	fmt.Fprintln(j)
	conclusion, err := parent.RunAgentTask(ctx, mergerAgentID,
		mergeTaskText(sources, initiatorNote, targetBranch, integration.Path), integration.Path, j)
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
	// 来源校验（2026-10 升级）：集成分支上新增的每个提交都必须可归因于某个源分支
	//——merger 的提示词约束是软的，这里是机器判据；发现越界提交执行硬回滚。
	suspects, verr := auditMergeProvenance(ctx, integration.Path, oldHead, targetBranch, sources)
	if verr != nil {
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "校验合并结果来源失败: "+mergeClip(verr.Error()))
		return
	}
	if len(suspects) > 0 {
		detail := mergeSuspectDetail(oldHead, suspects)
		if rerr := project.ResetHard(ctx, integration.Path, oldHead); rerr != nil {
			detail += "；回滚失败: " + rerr.Error() + "（集成分支仍停在越界状态，需人工处理）"
		} else {
			detail += fmt.Sprintf("；集成分支已回滚到 %s", mergeShortHash(oldHead))
		}
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, mergeClip(detail))
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

// auditMergeProvenance 是收尾来源校验：列出 oldHead..targetBranch 之间的每个提交，
// 判定它是否可归因于某个源分支。判据（从简，两步）：
//  1. 提交可达自某个源分支（IsAncestor(提交, 源分支)）——merge 带进来的源提交
//     都在源分支的历史上，这一条覆盖绝大多数提交；
//  2. 该提交是 merge commit 且第二父可达自某个源分支——merger 做合并产生的
//     合并提交本身不在任何源分支上，但它的第二父就是被合入的源分支头。
//
// 已知局限（刻意从简，不过度工程）：merger 在合并之外**单独提交**的非合并提交
//（单父、内容只涉及源分支文件的手工修补）会被判为越界——宁可误报回滚（任务可重试，
// 回滚不丢任何源分支改动），也不放过静默混进来的无关改动。
func auditMergeProvenance(ctx context.Context, integrationPath, oldHead, targetBranch string, sources []mergeSourceBranch) ([]mergeSuspect, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	commits, err := project.CommitRange(ctx, integrationPath, oldHead, targetBranch)
	if err != nil {
		return nil, err
	}
	var suspects []mergeSuspect
	for _, h := range commits {
		ok, err := commitAttributable(ctx, integrationPath, h, sources)
		if err != nil {
			return nil, err
		}
		if ok {
			continue
		}
		subject, serr := project.CommitSubject(ctx, integrationPath, h)
		if serr != nil {
			return nil, serr
		}
		files, ferr := project.CommitChangedFiles(ctx, integrationPath, h)
		if ferr != nil {
			return nil, ferr
		}
		suspects = append(suspects, mergeSuspect{Hash: h, Subject: subject, Files: files})
	}
	return suspects, nil
}

// commitAttributable 判定单个提交是否可归因于某个源分支（见 auditMergeProvenance）。
func commitAttributable(ctx context.Context, integrationPath, commit string, sources []mergeSourceBranch) (bool, error) {
	for _, src := range sources {
		if anc, err := project.IsAncestor(ctx, integrationPath, commit, src.Branch); err != nil {
			return false, err
		} else if anc {
			return true, nil
		}
	}
	// 不在任何源分支历史上：再看是不是 merge commit（第二父可达自某源分支）。
	parents, err := project.CommitParents(ctx, integrationPath, commit)
	if err != nil {
		return false, err
	}
	if len(parents) >= 2 {
		for _, src := range sources {
			if anc, err := project.IsAncestor(ctx, integrationPath, parents[1], src.Branch); err != nil {
				return false, err
			} else if anc {
				return true, nil
			}
		}
	}
	return false, nil
}

// mergeSuspect 是一个不可归因的提交（失败 detail 用）。
type mergeSuspect struct {
	Hash    string
	Subject string
	Files   []string
}

// mergeSuspectDetail 组来源校验失败的 detail：可疑提交清单（hash + 标题 + 改动文件）
// ——让人一眼看出越界范围；回滚结果由调用方追加。
func mergeSuspectDetail(oldHead string, suspects []mergeSuspect) string {
	var b strings.Builder
	fmt.Fprintf(&b, "合并结果校验失败：集成分支上有 %d 个提交无法归因于任何源分支（合并范围越界）", len(suspects))
	for _, sus := range suspects {
		fmt.Fprintf(&b, "\n- %s %s", mergeShortHash(sus.Hash), sus.Subject)
		if len(sus.Files) > 0 {
			fmt.Fprintf(&b, "（改动: %s）", strings.Join(sus.Files, ", "))
		}
	}
	return b.String()
}

// mergeShortHash 取提交 hash 前 7 位（detail 展示用；长度不足原样返回）。
func mergeShortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// forgetMergeProject 注销合并任务的项目登记（按项目互斥的清理）。
func (s *Server) forgetMergeProject(jobID string) {
	s.mergeMu.Lock()
	delete(s.mergeJobProject, jobID)
	s.mergeMu.Unlock()
}

// mergeEndedBy 取任务当前的结束方（Kill 时 Manager 已记下）；空 = 回落 self。
func mergeEndedBy(j jobs.Job) jobs.EndedBy {
	if by := j.Snapshot().EndedBy; by != "" {
		return by
	}
	return jobs.EndedSelf
}

// mergeSourceLines 渲染任务说明书的源分支清单（逐行：分支、会话、ahead、dirty）。
func mergeSourceLines(sources []mergeSourceBranch) string {
	lines := make([]string, 0, len(sources))
	for _, src := range sources {
		note := ""
		if src.Archived {
			note = "，已归档"
		}
		dirty := "干净"
		if src.Dirty > 0 {
			dirty = fmt.Sprintf("%d 条未提交改动", src.Dirty)
		}
		lines = append(lines, fmt.Sprintf("- %s（会话「%s」%s）：领先 %d 个提交，工作树 %s",
			src.Branch, src.Title, note, src.Ahead, dirty))
	}
	return strings.Join(lines, "\n")
}

// mergeTaskText 组给合并 Agent 的任务说明书（自包含：分支清单、范围硬限定、
// 路径、纪律与验收标准）。
func mergeTaskText(sources []mergeSourceBranch, initiatorNote, targetBranch, integrationPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `把下列源会话分支的改动合并到目标分支 %s。

工作位置（就在这里工作，不要动别处）：%s
- 你已经在目标分支 %s 的专用工作树里；**绝不在主检出里 merge，也不 push**

范围硬限定：本次合并只允许涉及下列源分支与目标分支 %s，它们都属于同一个项目。除此之外：不碰主检出、不碰其他项目/目录、**不把清单之外的任何文件或提交带进目标分支**——你在集成工作树里做的任何提交，内容必须来自对这些源分支的合并或为解决冲突所做的最小修改。

源分支清单（按此顺序逐个合并）：
%s
`, targetBranch, integrationPath, targetBranch, targetBranch, mergeSourceLines(sources))
	if initiatorNote != "" {
		fmt.Fprintf(&b, "（%s，不在合并清单里）\n", initiatorNote)
	}
	b.WriteString(`
合并纪律：
1. 逐个分支顺序合并（按清单顺序）：每个分支合并前先看该分支与目标分支的差异（git log / git diff），再动手合并——不盲目 merge
2. 一个分支合并完成并确认无冲突后，再合下一个；全部完成后统一跑构建与测试（用 bash），按仓库的验收标准验证
3. 冲突必须逐个解决并说明取舍：为什么保留这一边，另一边的意图如何被满足
4. **绝不用 --force、-X theirs / -X ours 掩盖冲突**——那是把别人的改动悄悄丢掉
5. 任何一步失败就把目标分支恢复到本次任务开始前的提交（git merge --abort 或 git reset --hard 到合并前），并**如实报告**：列出已成功合并与尚未开始的分支，绝不谎报成功
6. 不丢弃任何人的改动：源分支与目标分支上的提交都不许改写

最后给出结论：每个分支是否合并成功、冲突文件与取舍、构建测试结果。`)
	return b.String()
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
