package protocol

// ---- 后端日志（桌面错误日志查看器，2026-12）----
//
// logs.tail 读取后端**当前日志文件的尾部**——桌面壳内直跑形态与服务形态都
// 经 setupFileLogging 落盘（io.MultiWriter 同时回显 stderr，控制台行为不变），
// 设置页「日志」分区用它展示。只读当前文件：轮转掉的旧文件（.1/.2/.3）不
// 带出——用户要看的是「最近发生了什么」，不是考古。

// MethodLogsTail 读后端日志尾（参数 bytes 缺省 16KB）。
const MethodLogsTail = "logs.tail"

// LogsTailParams 是 logs.tail 的参数。Bytes 是要读的尾部字节数；0 = 缺省
// （16KB）——与 job.log 的「无游标全量」不同，日志是持续增长的，读尾即可。
type LogsTailParams struct {
	Bytes int `json:"bytes,omitempty"`
}

// LogsTailResult 是日志尾：Content 是截出的字节（可能跨行截断，前端原样
// 展示）；Truncated = 文件比请求的字节数大（读的只是尾部）；Path 是当前
// 日志文件路径（展示用——用户要能找到日志在哪）。
type LogsTailResult struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}
