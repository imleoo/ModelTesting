package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// corsMiddleware 允许 horizon-next 前端（跑在独立的 Next.js dev/生产端口，
// 和 api-server 不同源）用浏览器 fetch 直接调用这些接口。首版是内部工具，
// 不使用 Cookie/凭证式跨域请求（鉴权走 Authorization 头，不是 Cookie），
// 允许任意来源不会削弱 AuthMiddleware 已经提供的访问控制——真正的访问
// 控制仍然是 -auth-token，这里只是放开浏览器的同源限制。
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
