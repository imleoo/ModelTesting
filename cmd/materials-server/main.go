// materials-server 以 HTTP 静态文件形式对外提供多模态确定性素材（suites/<suite>/materials/），
// 供 deterministic_multimodal_qa 用例的 url 变体（Image_url / Video_url）在请求体中引用。
//
// 冻结的 URL 路径规则：/materials/<suite_id>/v1/<file>
// base_url（协议+主机+端口）是环境相关配置，本二进制不写死；本地开发默认监听 :8080。
//
// 实际处理逻辑在 internal/materialssrv 包，本文件只是命令行入口，
// 便于 P1 集成测试通过 httptest.NewServer 直接复用同一份处理器（见该包文档注释）。
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/leoobai/modeltestbed/internal/materialssrv"
)

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	root := flag.String("root", "suites", "套件根目录（其下按 <suite_id>/materials/ 存放素材）")
	flag.Parse()

	handler, err := materialssrv.NewHandler(*root)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("materials-server listening on %s, root=%s (path scheme: /materials/<suite_id>/v1/<file>)", *addr, *root)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
