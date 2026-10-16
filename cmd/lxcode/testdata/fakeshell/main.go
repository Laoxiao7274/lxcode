// fakeshell —— 更新引导替换单测的「假壳」：一个最小 main，按参数分支：
//
//	-selftest <标记文件>   写标记文件后退出（验证引导真的拉起了壳进程）
//	-hold <文件> <标记> <秒>  打开文件持句柄 N 秒（模拟壳映射 asar 的占用）
//
// 独立包的原因：`go build` 不含 _test.go，自举分支不能放在测试文件里。
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "-selftest" {
		_ = os.WriteFile(os.Args[2], []byte("launched"), 0o644)
		os.Exit(0)
	}
	if len(os.Args) >= 5 && os.Args[1] == "-hold" {
		if f, err := os.OpenFile(os.Args[2], os.O_RDONLY, 0); err == nil {
			_ = os.WriteFile(os.Args[3], []byte("holding"), 0o644)
			n, _ := strconv.Atoi(os.Args[4])
			time.Sleep(time.Duration(n) * time.Second)
			f.Close()
		}
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, "fakeshell: 参数不对")
	os.Exit(2)
}
