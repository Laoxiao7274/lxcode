package tools

import "testing"

func TestHighRiskBashCommand(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want string // 期望命中模式名；空 = 期望不命中
	}{
		// ---- 命中 ----
		{name: "git push origin main", cmd: "git push origin main", want: "git push"},
		{name: "git push --force", cmd: "git push --force", want: "git push"},
		{name: "大写 RM -RF 家目录", cmd: "RM -RF ~/", want: "rm -rf（家目录/系统根/通配）"},
		{name: "rm -rf 系统根", cmd: "rm -rf /", want: "rm -rf（系统根/通配）"},
		{name: "rm -rf 通配根", cmd: "rm -rf /*", want: "rm -rf（系统根/通配）"},
		{name: "reg add HKLM", cmd: "reg add HKLM\\SOFTWARE /v X /d Y", want: "reg add"},
		{name: "Remove-Item 大小写混合", cmd: "Remove-Item -Recurse -Force C:\\", want: "remove-item -recurse -force"},
		{name: "npm uninstall -g", cmd: "npm uninstall -g foo", want: "npm uninstall -g"},
		{name: "复合命令 go build && git push", cmd: "go build && git push", want: "git push"},
		{name: "管道中的高危段", cmd: "ls | rm -rf /", want: "rm -rf（系统根/通配）"},
		{name: "分号分隔的高危段", cmd: "echo hi; reg delete HKLM\\Run", want: "reg delete"},
		{name: "环境变量前缀不遮蔽高危", cmd: "FOO=1 git push origin main", want: "git push"},
		{name: "换行分隔的高危段", cmd: "go build\nrm -rf /", want: "rm -rf（系统根/通配）"},
		{name: "pip uninstall", cmd: "pip uninstall requests", want: "pip uninstall"},
		{name: "format", cmd: "format D: /Q", want: "format"},
		{name: "mkfs", cmd: "mkfs.ext4 /dev/sda1", want: "mkfs"},
		{name: "diskpart", cmd: "diskpart", want: "diskpart"},

		// ---- 不误伤 ----
		{name: "go build", cmd: "go build ./...", want: ""},
		{name: "npm test", cmd: "npm test", want: ""},
		{name: "git status", cmd: "git status", want: ""},
		{name: "git add", cmd: "git add -A", want: ""},
		{name: "rm file.txt", cmd: "rm file.txt", want: ""},
		{name: "echo git push（字面参数）", cmd: "echo git push", want: ""},
		{name: "grep rm -rf（字面参数）", cmd: "grep 'rm -rf' x.txt", want: ""},
		{name: "echo 后复合高危仍应命中", cmd: "echo hi && git push", want: "git push"},
		{name: "printf 字面参数", cmd: "printf rm -rf /", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := HighRiskBashCommand(tc.cmd)
			if tc.want == "" {
				if ok {
					t.Fatalf("期望不命中，实际命中 %q", got)
				}
				return
			}
			if !ok {
				t.Fatalf("期望命中 %q，实际未命中", tc.want)
			}
			if got != tc.want {
				t.Fatalf("期望命中 %q，实际命中 %q", tc.want, got)
			}
		})
	}
}
