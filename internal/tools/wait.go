package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
)

// wait 工具：阻塞等一个后台任务结束（或满足条件），**只回状态不回输出**。
//
// 与 job_output(wait=true) 的核心差异：job_output 的等待返回携带着输出增量
//（等待期间可能又写了输出，它会把增量补上），而 wait 的场景是「只要知道它
// 结束了/成功了，下一步再决定要不要看输出」——返回只带一行状态，省 token。
// 要输出时再调 job_output（任务结束后仍可读，完整日志在 OutputPath）。
//
// 复用 waitJob 原语（jobs.go：订阅 Manager 事件 + 快照判定，五谓词同款）——
// 不另写一套等待循环（两条等待路径漂移的代价是同一条件一处命中一处不中）。
// 先 Snapshot 判终态立即返回（已结束的任务零等待），未终态才进阻塞等待。
//
// RiskLow 不走确认门（与 job 三件套同族：job_kill 杀的都是自己起的进程，
// wait 只是读状态）。condition 参数与 job_output 的 wait_condition 同款
//（exit/exit_ok/output/idle/{pattern}，OR 组合）——解析走同一个
// parseWaitCondition，语义一处定义。
const waitToolName = "wait"

func waitDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"job_id": {"type": "string", "description": "任务 id（bash 的 run_in_background 返回，或 job_list 里的）"},
			"timeout_sec": {"type": "integer", "description": "等待上限秒数（默认 30，上限 600）；超时返回当前状态"},
			"condition": {"oneOf": [
				{"type": "string", "enum": ["exit", "exit_ok", "output", "idle"]},
				{"type": "object", "properties": {"pattern": {"type": "string"}}, "required": ["pattern"], "additionalProperties": false}
			], "description": "等什么（缺省 = exit 任务结束）：exit_ok=等退出码 0；output=有新输出即返回；idle=输出静默 idle_ms（默认 5000）；{\"pattern\": \"…\"}=全量输出日志命中正则即返回。多个条件 OR 组合（数组），谁先到返回谁"}
		},
		"required": ["job_id"]
	}`)
	return &Def{
		Name: waitToolName,
		Description: "阻塞等待一个后台任务（bash 的 run_in_background）结束或满足条件，**只返回状态不返回输出**" +
			"（要输出再调 job_output）——「等它跑完再继续」用这个，比 job_output(wait=true) 省 token。" +
			"condition 与 job_output 的 wait_condition 同款：exit（默认，等结束）/ exit_ok（等退出码 0）" +
			"/ output（等新输出）/ idle（等静默）/ {\"pattern\":\"…\"}（等输出命中正则）；" +
			"超时返回当前状态（任务还在跑就如实报 running）。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				JobID      string          `json:"job_id"`
				TimeoutSec int             `json:"timeout_sec"`
				Condition  json.RawMessage `json:"condition"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			id := strings.TrimSpace(a.JobID)
			if id == "" {
				return "", fmt.Errorf("job_id 不能为空（用 job_list 看有哪些任务）")
			}
			if a.TimeoutSec < 0 {
				return "", fmt.Errorf("timeout_sec 不能为负")
			}
			cond, err := parseWaitCondition(a.Condition, 0)
			if err != nil {
				return "", err
			}
			mgr := r.getJobs()
			if mgr == nil {
				return "", fmt.Errorf("后台任务未装配（后端未初始化任务管理器）")
			}
			// 存在性校验在进等待之前：waitJob 对未知任务只会白等到超时
			//（jobOutputDef 的 Read 路径自带「任务不存在」报错，wait 没有
			// Read 这一步，校验要自己做）
			snap, err := mgr.Snapshot(id)
			if err != nil {
				return "", err
			}
			// 终态立即返回（不进等待循环）：job_output 也这样，但 wait 没有
			// 「顺便把增量补上」的副业，直接快照就够
			if snap.Status.Terminal() {
				return formatWaitResult(snap, nil), nil
			}
			timeout := jobWaitDefault
			if a.TimeoutSec > 0 {
				timeout = time.Duration(min(a.TimeoutSec, int(jobWaitMax/time.Second))) * time.Second
			}
			// wait 不关心 output 基线（不像 job_output 的 output 谓词要按游标比增量
			//——wait 的语义是「有输出就算」），基线给 0：只要任务写过输出就命中。
			// 执行会话通告口照 job_output 注入（子会话等任务结束同样要被点亮）。
			snap, hit := waitJob(ctx, mgr, id, timeout, cond, 0, JobNotify(ctx))
			return formatWaitResult(snap, hit), nil
		},
	}
}

// formatWaitResult 渲染 wait 的返回：一行状态（不带输出——省 token 是本工具
// 存在的理由）。hit != nil 时带命中说明（与 job_output 同款措辞），模型要能
// 区分「等到了 exit」和「等到了 pattern 命中」。
func formatWaitResult(snap jobs.Snapshot, hit *waitCondHit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "任务 %s（%s）状态: %s", snap.ID, snap.Label, snap.Status)
	if snap.Status.Terminal() {
		fmt.Fprintf(&b, "（结束方: %s", snap.EndedBy)
		if snap.Detail != "" {
			fmt.Fprintf(&b, "，%s", snap.Detail)
		}
		b.WriteString("）")
	} else {
		b.WriteString("（仍在运行——超时返回，未等到目标条件；要继续等再调 wait 或用 job_output(wait=true)）")
	}
	b.WriteString("\n（输出未随结果返回——要看输出/读日志用 job_output）")
	if hit != nil {
		switch hit.kind {
		case "exit":
			b.WriteString("\n[hit: 等到了任务结束]")
		case "exit_ok":
			b.WriteString("\n[hit: 等到了任务成功退出]")
		case "output":
			b.WriteString("\n[hit: 有新输出]")
		case "idle":
			b.WriteString("\n[hit: 输出已静默]")
		case "pattern":
			fmt.Fprintf(&b, "\n[hit: 输出命中 %s → %s]", hit.pattern, hit.line)
		}
	}
	return b.String()
}
