package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// authMiddleware 实现设计方案 10.1 节鉴权行"固定 Token / Basic Auth，内网
// 访问"里的固定 Token 方案——这是首版明确要求的最低限度鉴权，不是像
// 10.2 节 JWT 体系那样按需后置的能力。token 为空时中间件直接放行，供本地
// 开发/测试场景使用；生产部署必须通过 -auth-token 传一个非空值，否则
// 整个 API 对任何能访问到这个端口的人完全开放。
func authMiddleware(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		header := c.GetHeader("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed Authorization header"})
			return
		}
		supplied := strings.TrimPrefix(header, prefix)
		// subtle.ConstantTimeCompare 避免逐字节比较在计时侧信道上泄露 token
		// 前缀信息——固定 token 场景下这个防护成本很低，没有理由不加。
		if len(supplied) != len(token) || subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Next()
	}
}
