// Package chatmsg 是全系统的消息事实模型（叶包，零内部依赖——与 sessiondata
// 同一定位）：Message / ToolCall / AttachmentRef / ImageRef 及附件白名单常量。
//
// 为什么独立成包：这些类型是 store 落库、tools 执行签名、protocol wire、agent
// 内核、llm 适配器共用的「消息长什么样」，原先住在 llm（LLM 传输客户端）包里，
// 造成 store→llm、tools→llm 的语义倒挂——存储与工具面并不关心 wire 客户端，
// 却被迫 import 它才能拿到消息类型。搬到这里后依赖方向收敛为 llm→chatmsg
// （llm 保留 wire 客户端/适配逻辑，经类型别名过渡，既有引用不用改）。
//
// 纪律：本包不得 import 仓库内任何其他包（boundaries_test 钉住）。
package chatmsg

// 附件引用的两种展示形态：图片渲染内联缩略图（点击放大），其余渲染文件卡
// （图标 + 名字 + 大小，点击下载）。前端按它分流，后端按它决定落盘通道。
const (
	AttachmentKindImage = "image"
	AttachmentKindFile  = "file"
)

// 视觉请求的硬限制（与协议/server 侧共用同一份常量——各写一遍必然漂移）：
//   - 单张 ≤5MB：Anthropic 对 base64 源图片的上限（解码后字节数）；
//   - 一条消息 ≤4 张：两家端点的通用安全上限。
const (
	MaxImageBytes       = 5 << 20 // 5MB（base64 解码后）
	MaxImagesPerMessage = 4
)

// MaxFileAttachmentBytes 单个**文件**附件的大小上限（落盘字节）：20MB。
// 与图片的 5MB（Anthropic 对 base64 源图的上限）分开——文件只落盘（给
// read_file 读 / share_file 共享），不受端点限制，给到「日常文档」够用的量级。
// 放本包与 MaxImageBytes 同一份常量池：tools（share_file 的前置检查）、
// store（写入边界）、server（上传校验）三层共用，各写一遍必然漂移。
const MaxFileAttachmentBytes = 20 << 20

// imageMIMEs 是 mime 白名单（png/jpeg/webp/gif——两家端点都支持的视觉格式）。
var imageMIMEs = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// ValidImageMime 判断 mime 是否在视觉白名单内。
func ValidImageMime(m string) bool { return imageMIMEs[m] }

// ImageRef 是消息携带的图片**引用**：Path 是相对附件根目录的路径
// （如 "<session-id>/<随机id>.png"），Mime 是图片类型（白名单内）。
// 恒为引用：base64 绝不进这个结构（分层纪律：历史与内存恒存文件引用，
// base64 只在构造 LLM 请求的瞬间存在——不发库、不进长期内存、不进日志）。
type ImageRef struct {
	Path string `json:"path"`
	Mime string `json:"mime"`
}

// AttachmentRef 是消息携带的一个附件的**引用 + 展示元数据**（不进模型上下文）：
//   - Path：相对 attachments 根的路径（"<session-id>/<随机id>.<ext>"——磁盘名
//     恒为随机 hex，不使用原名）；
//   - Name：展示名（净化后的原名；客户端没给时服务端回落"图片"/"file"）；
//   - Size：字节数（前端文件卡显示人话大小；未知 = 0，卡片不显示大小）；
//   - Mime：图片类型（kind=image 时恒有；文件为空——文件不做 mime 白名单）；
//   - Caption：共享者附的一句话说明（share_file 的可选文案；用户消息恒空）。
//
// 恒为引用：base64 绝不进这个结构（与 ImageRef 同一条分层纪律）。
type AttachmentRef struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mime    string `json:"mime,omitempty"`
	Kind    string `json:"kind"`
	Caption string `json:"caption,omitempty"`
}

// imageLoader 由宿主注入的图片读取函数：给一个引用，返回 (mime, 原始字节, 错误)。
// nil = 未注入（纯内存调用方/单测）——按"读取失败"走 fail-open。
// 本包不知道附件落在磁盘哪里，由宿主（server）在装配期注入读取函数；
// 注入的是"读文件"这个最小能力，路径解析与穿越防护都在调用方（store/server）。
var imageLoader func(ref ImageRef) (mime string, data []byte, err error)

// SetImageLoader 注入图片读取函数（进程级一次；server 装配期绑定到附件根目录）。
func SetImageLoader(fn func(ref ImageRef) (mime string, data []byte, err error)) {
	imageLoader = fn
}

// LoadImage 读一张图片引用（fail-open 的调用方用：未注入/读失败都返回错误，
// 由调用方决定怎么降级）。llm 在构造请求的瞬间经它取字节，base64 只活在
// 那个请求体里，绝不写回 Message.Images。
func LoadImage(ref ImageRef) (mime string, data []byte, err error) {
	if imageLoader == nil {
		return "", nil, errImageLoaderNotSet
	}
	return imageLoader(ref)
}

// ImageLoader 返回当前注入的读取函数（nil = 未注入；测试保存/恢复用）。
func ImageLoader() func(ref ImageRef) (mime string, data []byte, err error) {
	return imageLoader
}

// errImageLoaderNotSet 是未注入 loader 时的错误（调用方按"读取失败"降级）。
var errImageLoaderNotSet = errorImageLoaderNotSet{}

type errorImageLoaderNotSet struct{}

func (errorImageLoaderNotSet) Error() string { return "图片读取函数未注入" }
