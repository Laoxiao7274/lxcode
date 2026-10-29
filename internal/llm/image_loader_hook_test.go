package llm

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/chatmsg"
)

// withTestImageLoader 换掉包级图片读取函数、测完恢复原状（loader 是进程级
// 单例——串行测试共享，各自保存/恢复避免互相污染）。loader 变量本体住在
// chatmsg（类型搬家后的归属），llm 侧经这个 hook 存取。
func withTestImageLoader(t *testing.T, fn func(ref ImageRef) (string, []byte, error)) {
	t.Helper()
	old := chatmsg.ImageLoader()
	chatmsg.SetImageLoader(fn)
	t.Cleanup(func() { chatmsg.SetImageLoader(old) })
}
