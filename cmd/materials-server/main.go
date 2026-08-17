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
	// absRoot 本身的路径里也可能含符号链接（如 macOS /tmp -> /private/tmp）；
	// 预先解析出真实路径，后续符号链接边界校验统一与这个真实路径比较，
	// 避免绝对路径的字面形式和 EvalSymlinks 解析结果不一致导致误判。
	absRootReal, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		log.Fatalf("resolve real root: %v", err)
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
		// 上面两次 filepathHasPrefix 只是词法比较，不解析符号链接：如果素材目录里
		// 存在指向目录外的符号链接，词法上仍在 absRoot 前缀内，但 http.ServeFile
		// 最终由操作系统解析符号链接实际读取的文件可能已经越出 absRoot。
		// EvalSymlinks 把路径解析到真实文件系统位置后再校验一次同一边界；
		// 若目标文件不存在（EvalSymlinks 报错），跳过校验，交给 ServeFile 走正常 404。
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
