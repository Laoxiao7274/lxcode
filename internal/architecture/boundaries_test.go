// Package architecture 验证运行时代码的单向依赖；编排脚本与测试可装配两端。
package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// skipDir 是与 Go 源无关的目录（node_modules 一个就能让遍历慢一个量级——
// 冷缓存下实测风险；.git 同理）。
var skipDir = map[string]bool{
	"node_modules": true,
	".git":         true,
	"release":      true,
	"dist":         true,
	"bin":          true,
}

func TestBackendDependencyBoundaries(t *testing.T) {
	const prefix = "github.com/moyunteng/lxcode/"
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir[entry.Name()] && path != ".." {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		local, err := filepath.Rel("..", path)
		if err != nil {
			return err
		}
		pkg := strings.Split(filepath.ToSlash(local), "/")[0]
		for _, spec := range file.Imports {
			dep, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(dep, prefix+"frontend") || strings.HasPrefix(dep, prefix+"shell") {
				t.Errorf("%s: 后端不得依赖客户端实现 %s", path, dep)
			}
			if pkg == "agent" && (dep == prefix+"internal/store" || dep == prefix+"internal/server" || dep == prefix+"internal/protocol" || dep == "database/sql") {
				t.Errorf("%s: agent 应依赖业务接口而不是存储或传输实现 %s", path, dep)
			}
			if pkg == "store" && (dep == prefix+"internal/agent" || dep == prefix+"internal/server" || dep == prefix+"internal/protocol") {
				t.Errorf("%s: 存储不得反向依赖运行时或传输层 %s", path, dep)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
