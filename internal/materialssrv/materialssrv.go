// Package materialssrv 实现多模态确定性素材的 HTTP 静态托管处理器
// （冻结路径规则 /materials/<suite_id>/v1/<file>，见 suites/kimi-k3/materials/manifest.json）。
// 从 cmd/materials-server 抽出，便于 P1 集成测试直接复用同一份处理器逻辑
// （httptest.NewServer 包一层即可），不需要另外拉起独立进程。
// 安全边界的设计与验证过程见 P0 阶段 commit af40bb7、6b56546 的提交说明。
package materialssrv

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var suiteIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// NewHandler 构造服务 root 目录下素材的 http.Handler。root 其下按
// <suite_id>/materials/ 存放素材文件。
func NewHandler(root string) (http.Handler, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	if _, err := os.Stat(absRoot); err != nil {
		return nil, fmt.Errorf("suites root %q 不存在: %w", absRoot, err)
	}
	absRootReal, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve real root: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/materials/", func(w http.ResponseWriter, r *http.Request) {
		rest := r.URL.Path[len("/materials/"):]
		parts := splitFirstTwo(rest)
		if parts == nil {
			http.NotFound(w, r)
			return
		}
		suiteID, version, file := parts[0], parts[1], parts[2]
		if version != "v1" {
			http.NotFound(w, r)
			return
		}
		if !suiteIDPattern.MatchString(suiteID) {
			http.Error(w, "invalid suite id", http.StatusBadRequest)
			return
		}
		suiteDir := filepath.Join(absRoot, suiteID, "materials")
		full := filepath.Join(suiteDir, file)
		if !filepathHasPrefix(full, suiteDir) || !filepathHasPrefix(full, absRoot) {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		if resolved, err := filepath.EvalSymlinks(full); err == nil {
			if !filepathHasPrefix(resolved, absRootReal) {
				http.Error(w, "invalid path", http.StatusBadRequest)
				return
			}
			full = resolved
		}
		http.ServeFile(w, r, full)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux, nil
}

func splitFirstTwo(rest string) []string {
	i := indexByte(rest, '/')
	if i < 0 {
		return nil
	}
	suiteID := rest[:i]
	remainder := rest[i+1:]
	j := indexByte(remainder, '/')
	if j < 0 {
		return nil
	}
	version := remainder[:j]
	file := remainder[j+1:]
	if suiteID == "" || version == "" || file == "" {
		return nil
	}
	return []string{suiteID, version, file}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func filepathHasPrefix(path, prefix string) bool {
	rel, err := filepath.Rel(prefix, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
