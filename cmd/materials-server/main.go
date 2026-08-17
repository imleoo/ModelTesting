// materials-server 以 HTTP 静态文件形式对外提供多模态确定性素材（suites/<suite>/materials/），
// 供 deterministic_multimodal_qa 用例的 url 变体（Image_url / Video_url）在请求体中引用。
//
// 冻结的 URL 路径规则：/materials/<suite_id>/v1/<file>
// base_url（协议+主机+端口）是环境相关配置，本二进制不写死；本地开发默认监听 :8080。
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// suiteID 只允许字母数字下划线连字符，明确拒绝 "."、".."、"/" 等路径元字符，
// 防止 suiteID 本身被用来构造穿越路径（suiteID 会同时参与拼接目标路径和越权校验的前缀，
// 若不在此处收紧，两者会一起偏移，越权检查形同虚设）。
var suiteIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	root := flag.String("root", "suites", "套件根目录（其下按 <suite_id>/materials/ 存放素材）")
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		log.Fatalf("resolve root: %v", err)
	}
	if _, err := os.Stat(absRoot); err != nil {
		log.Fatalf("suites root %q 不存在: %v", absRoot, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/materials/", func(w http.ResponseWriter, r *http.Request) {
		// /materials/<suite_id>/v1/<file> -> <root>/<suite_id>/materials/<file>
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
		// 双重边界校验：suiteID 已被 suiteIDPattern 限制为不含 "."/".."/"/"，
		// suiteDir 因此保证是 absRoot 下的真实子目录；这里再分别校验 full 未逃出
		// suiteDir（挡住 file 里的穿越片段）和未逃出 absRoot（纵深防御，不单独信任
		// 由用户输入 suiteID 参与构造的 suiteDir 作为唯一边界）。
		if !filepathHasPrefix(full, suiteDir) || !filepathHasPrefix(full, absRoot) {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		http.ServeFile(w, r, full)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("materials-server listening on %s, serving root=%s (path scheme: /materials/<suite_id>/v1/<file>)", *addr, absRoot)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func splitFirstTwo(rest string) []string {
	// rest 形如 "<suite_id>/v1/<file...>"
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
