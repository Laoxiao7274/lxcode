// 图片附件（视觉请求）的读侧支撑：包级 loader 注入 + 请求构造瞬间的编码。
//
// 分层纪律（2026-10 用户拍板）：历史与内存恒存**文件引用**（相对路径），
// base64 只在构造 LLM 请求的瞬间存在——不发库、不进长期内存历史、不进日志。
// 引用类型（ImageRef/AttachmentRef）、白名单常量与 loader 注入住在
// internal/chatmsg（叶包）；本文件只保留请求构造瞬间的编码（这是 wire 侧的事）。
package llm

import (
	"encoding/base64"
	"fmt"

	"github.com/moyunteng/lxcode/internal/chatmsg"
)

// 附件类型/常量/白名单/ loader 注入经别名与包装过渡（语义零变化，既有引用
// 不用改）：ImageRef/AttachmentRef/AttachmentKind*、MaxImageBytes、
// MaxImagesPerMessage、MaxFileAttachmentBytes、ValidImageMime、SetImageLoader
// 全部转发到 chatmsg。llm 之后只允许引用 chatmsg（方向：llm→chatmsg），不反向。
type (
	ImageRef      = chatmsg.ImageRef
	AttachmentRef = chatmsg.AttachmentRef
)

const (
	AttachmentKindImage    = chatmsg.AttachmentKindImage
	AttachmentKindFile     = chatmsg.AttachmentKindFile
	MaxImageBytes          = chatmsg.MaxImageBytes
	MaxImagesPerMessage    = chatmsg.MaxImagesPerMessage
	MaxFileAttachmentBytes = chatmsg.MaxFileAttachmentBytes
)

// ValidImageMime 判断 mime 是否在视觉白名单内（转发 chatmsg，别名过渡）。
func ValidImageMime(m string) bool { return chatmsg.ValidImageMime(m) }

// SetImageLoader 注入图片读取函数（进程级一次；server 装配期绑定到附件根目录）。
// 转发 chatmsg 的注入点。
func SetImageLoader(fn func(ref ImageRef) (mime string, data []byte, err error)) {
	chatmsg.SetImageLoader(fn)
}

// loadedImage 是请求构造瞬间的编码产物：base64 只活在这里，随请求体一起被
// GC 回收，绝不写回 Message.Images。
type loadedImage struct {
	mime string
	b64  string
}

// loadImages 把一条消息的图片引用读出来并编码成 base64（fail-open）。
//
// fail-open 的理由：图片是**附件**，不是消息正文——磁盘上被清理/移动不该让
// 整轮请求失败（历史里那条引用还在，会话不能因此变砖）。读取失败的图跳过，
// 在文本尾部追加一条人话提示让模型知道"用户发过一张图但加载不了"。
// 返回 (编码成功的图, 追加到文本尾部的提示)。
func loadImages(refs []ImageRef) ([]loadedImage, string) {
	var imgs []loadedImage
	note := ""
	for _, ref := range refs {
		mime, data, err := chatmsg.LoadImage(ref)
		if err != nil || !ValidImageMime(mime) {
			// 错误文案不含图片内容（本来也拿不到），只有路径——路径不是敏感载荷
			note += fmt.Sprintf("\n[图片未能加载: %s]", ref.Path)
			continue
		}
		imgs = append(imgs, loadedImage{mime: mime, b64: base64.StdEncoding.EncodeToString(data)})
	}
	return imgs, note
}
