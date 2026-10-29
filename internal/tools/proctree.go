package tools

import "time"

// procKillGrace 是「进程本该收尾、但 I/O 管道还被攥着」的兜底等待。
//
// 为什么需要它：cmd.Wait 除了等进程退出，还要等 stdout/stderr 管道**写端全部
// 关闭**——而 sh -c 拉起的孙进程也持有那个写端。哪怕树杀已经生效，只要还有一个
// 逃逸进程攥着管道，Wait 就会一直阻塞，任务永远停在 stopping、结束通告永远发不
// 出去（用户点「结束」什么都不发生）。有了它，最坏也只是晚 procKillGrace 收尾：
// 后台任务的核心承诺（结束通告一定送达）不会被一个逃逸进程吃掉。
//
// 它**不是**任务寿命上限：计时器只在「ctx 结束」或「进程已退出」时才启动
// （见 os/exec.Cmd.WaitDelay 文档），健康的 dev server 不会被它杀掉。
const procKillGrace = 5 * time.Second
