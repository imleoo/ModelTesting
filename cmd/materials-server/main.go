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
	"strings"
)

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
		full := filepath.Join(absRoot, suiteID, "materials", file)
		if !filepathHasPrefix(full, filepath.Join(absRoot, suiteID, "materials")) {
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
