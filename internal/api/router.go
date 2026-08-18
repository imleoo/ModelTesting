package api

import "github.com/gin-gonic/gin"

// NewRouter 装配设计方案 10.1 节「API + 编排」层的全部路由，供 3 个
// horizon-next 页面（模型登记、发起任务+结果详情、报告下载，见 10.3 节
// 路由映射）消费。
func NewRouter(cfg *Config) *gin.Engine {
	r := gin.Default()

	api := r.Group("/api")
	{
		api.POST("/providers", cfg.CreateProvider)
		api.GET("/providers", cfg.ListProviders)

		api.POST("/models", cfg.CreateModel)
		api.GET("/models", cfg.ListModels)
		api.GET("/models/:id", cfg.GetModel)

		api.POST("/test-runs", cfg.LaunchTestRun)
		api.GET("/test-runs", cfg.ListTestRuns)
		api.GET("/test-runs/:id", cfg.GetTestRun)
		api.GET("/test-runs/:id/case-results", cfg.GetTestRunCaseResults)
		api.GET("/test-runs/:id/report", cfg.GetTestRunReport)
	}

	return r
}
