// 「这是不是 lxcode 本体仓库」判据的验收用例：两个指纹文件同时在场才算；
// 缺一个（或只是目录同名）都不算——假阳性的代价是用户项目的推送被无理由拒绝。
package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsSelfRepoRequiresBothMarkers：两个指纹文件都在场才认定为本体；
// 缺任何一个都返回 false（判据是「同时」而不是「任一」）。
func TestIsSelfRepoRequiresBothMarkers(t *testing.T) {
	cases := []struct {
		name   string
		files  []string
		wantIt bool
	}{
		{name: "两个特征都在", files: []string{selfRepoSyncScript, selfRepoHistoryDoc}, wantIt: true},
		{name: "只有发版脚本", files: []string{selfRepoSyncScript}, wantIt: false},
		{name: "只有历史契约文档", files: []string{selfRepoHistoryDoc}, wantIt: false},
		{name: "都没有（普通项目）", files: nil, wantIt: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, rel := range tc.files {
				writeFile(t, filepath.Join(root, rel), "// marker\n")
			}
			if got := IsSelfRepo(root); got != tc.wantIt {
				t.Fatalf("IsSelfRepo(%v) = %v, want %v", tc.files, got, tc.wantIt)
			}
		})
	}
}

// TestIsSelfRepoIgnoresDirectoryNamedLikeMarker：同名**目录**不算特征文件
// （os.Stat 成功但不是普通文件——目录不能当指纹）。
func TestIsSelfRepoIgnoresDirectoryNamedLikeMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, selfRepoSyncScript), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, selfRepoHistoryDoc), "doc\n")
	if IsSelfRepo(root) {
		t.Fatal("同名目录不该被当作指纹文件")
	}
}

// TestIsSelfRepoEmptyOrMissingDir：空路径与不存在的目录一律 false
// （判不了就当普通项目——闸门兜底是提示词纪律，不让所有推送失败）。
func TestIsSelfRepoEmptyOrMissingDir(t *testing.T) {
	if IsSelfRepo("") {
		t.Fatal("空路径不该认定为本体仓库")
	}
	if IsSelfRepo(filepath.Join(t.TempDir(), "nope")) {
		t.Fatal("不存在的目录不该认定为本体仓库")
	}
}

// TestSelfRepoReleaseCommandIsRunnable：错误文案里的指引必须是真实存在的命令
// （本仓库根有 scripts/push-release.mjs，且命令里点到了它）。
func TestSelfRepoReleaseCommandIsRunnable(t *testing.T) {
	if SelfRepoReleaseCommand == "" {
		t.Fatal("发版指引命令不该为空")
	}
	if !strings.Contains(SelfRepoReleaseCommand, filepath.Base(selfRepoSyncScript)) {
		t.Fatalf("发版指引应点名 %s: %q", selfRepoSyncScript, SelfRepoReleaseCommand)
	}
}
