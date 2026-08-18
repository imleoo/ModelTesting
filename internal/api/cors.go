package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// corsMiddleware 允许 horizon-next 前端（跑在独立的 Next.js dev/生产端口，
// 和 api-server 不同源）用浏览器 fetch 直接调用这些接口。首版是内部工具，
// 不使用 Cookie/凭证式跨域请求（鉴权走 Authorization 头，不是 Cookie），
// 允许任意来源本身不会让恶意页面窃取到 Bearer Token——浏览器不会替第三方
// 页面自动附加这个头，读到响应也需要该页面自己先持有正确的 token。
//
// 但这不等于"以后配置 -auth-token 也不用再改前端"：web/src/utils/apiClient.ts
// 目前完全不发送 Authorization 头（已知限制，见该文件顶部注释），一旦
// api-server 配置了 -auth-token，所有前端请求都会被 authMiddleware 拒绝
// （401），iframe/新标签页打开报告这类无法附加自定义头的场景更是天然无法
// 携带 token——这里只是放开浏览器的同源限制，不代表已经解决了"前端如何
// 安全地持有并附带 token"这个问题，那部分仍然是待办。
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
