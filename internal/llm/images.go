// 图片附件（视觉请求）的读侧支撑：包级 loader 注入 + 请求构造瞬间的编码。
//
// 分层纪律（2026-10 用户拍板）：历史与内存恒存**文件引用**（相对路径），
// base64 只在构造 LLM 请求的瞬间存在——不发库、不进长期内存历史、不进日志。
// llm 层不知道附件落在磁盘哪里，由宿主（server）在装配期注入读取函数；
// 注入的是"读文件"这个最小能力，路径解析与穿越防护都在调用方（store/server）。
package llm

import (
	"encoding/base64"
	"fmt"
)

// 视觉请求的硬限制（与协议/server 侧共用同一份常量——各写一遍必然漂移）：
//   - 单张 ≤5MB：Anthropic 对 base64 源图片的上限（解码后字节数）；
//   - 一条消息 ≤4 张：两家端点的通用安全上限。
const (
	MaxImageBytes       = 5 << 20 // 5MB（base64 解码后）
	MaxImagesPerMessage = 4
)

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
// 恒为引用：base64 绝不进这个结构（见文件头注释的分层纪律）。
type ImageRef struct {
	Path string `json:"path"`
	Mime string `json:"mime"`
}

// imageLoader 由宿主注入的图片读取函数：给一个引用，返回 (mime, 原始字节, 错误)。
// nil = 未注入（纯内存调用方/单测）——按"读取失败"走 fail-open。
var imageLoader func(ref ImageRef) (mime string, data []byte, err error)

// SetImageLoader 注入图片读取函数（进程级一次；server 装配期绑定到附件根目录）。
func SetImageLoader(fn func(ref ImageRef) (mime string, data []byte, err error)) {
	imageLoader = fn
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
		if imageLoader == nil {
			note += fmt.Sprintf("\n[图片未能加载: %s]", ref.Path)
			continue
		}
		mime, data, err := imageLoader(ref)
		if err != nil || !ValidImageMime(mime) {
			// 错误文案不含图片内容（本来也拿不到），只有路径——路径不是敏感载荷
			note += fmt.Sprintf("\n[图片未能加载: %s]", ref.Path)
			continue
		}
		imgs = append(imgs, loadedImage{mime: mime, b64: base64.StdEncoding.EncodeToString(data)})
	}
	return imgs, note
}
