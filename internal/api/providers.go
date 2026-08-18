package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/store"
)

// CreateProvider 对应 07 节 SOP 第 2 步"测试台登记供应商与模型"里的供应商
// 登记部分（horizon-next `/admin/providers` 页面）。
func (cfg *Config) CreateProvider(c *gin.Context) {
	var p model.Provider
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	created, err := cfg.Store.CreateProvider(p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (cfg *Config) ListProviders(c *gin.Context) {
	ps, err := cfg.Store.ListProviders()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ps)
}

// CreateModel 对应 07 节 SOP 第 2 步里 CAPABILITY_PROFILE 的录入部分。请求体
// 直接是 model.Model（含内嵌的 Capability），provider_id 必须指向一个已登记
// 的 Provider，CapabilityProfile 本身必须合法（image_base64/video_base64
// 不可标记不支持、thinking_toggle_methods 至少声明一种——校验逻辑在
// internal/store.CreateModel 里复用了 P1 就有的 model.ValidateCapabilityProfile）。
//
// 不含 API Key：能力声明和端点信息可以持久化，但测试用 API Key 按 12 节
// "应用层加密存储"的首版简化处理是完全不落盘——每次发起测试任务时随请求
// 临时传入（见 LaunchTestRun），只在那一次任务执行期间使用，不写数据库、
// 不写日志。这避免了在没有加密体系的首版里做一套"半成品加密存储"，比
// 落盘一份弱保护的密钥更安全。
func (cfg *Config) CreateModel(c *gin.Context) {
	var m model.Model
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	created, err := cfg.Store.CreateModel(m)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (cfg *Config) ListModels(c *gin.Context) {
	ms, err := cfg.Store.ListModels()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ms)
}

func (cfg *Config) GetModel(c *gin.Context) {
	m, err := cfg.Store.GetModel(c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, m)
}
