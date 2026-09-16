package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/store"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

// suiteNamePattern 约束套件名（既是 suites 表的唯一键，也是
// internal/materialssrv/materialssrv.go 素材托管 URL /materials/<name>/v1/
// <file> 里的 <name> 段，必须和那边的 suiteIDPattern 保持一致——名字一旦不
// 满足这个模式，套件即使建出来了，它的多模态素材 URL 也永远 404。
var suiteNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// suiteFileNamePattern 匹配套件定义文件名，如 "suite.v1.json"。只在
// SeedSuitesFromDisk 扫描 git 跟踪的种子目录时用到。
var suiteFileNamePattern = regexp.MustCompile(`^suite\.v[0-9]+\.json$`)

// SuiteSummary 是 GET /api/suites 返回的列表项，SuiteID 就是
// TestRun.suite_id 期望的值。
type SuiteSummary struct {
	SuiteID      string `json:"suite_id"`
	Name         string `json:"name"`
	SuiteVersion string `json:"suite_version"`
	CaseCount    int    `json:"case_count"`
}

func toSuiteSummary(suite model.Suite) (SuiteSummary, error) {
	parsed, err := suitedef.ParseSuite([]byte(suite.DefinitionJSON))
	if err != nil {
		return SuiteSummary{}, fmt.Errorf("解析套件 %q 的定义失败: %w", suite.Name, err)
	}
	return SuiteSummary{
		SuiteID:      suite.Name,
		Name:         parsed.SuiteID,
		SuiteVersion: parsed.SuiteVersion,
		CaseCount:    len(parsed.Cases),
	}, nil
}

// ListSuites 列出 suites 表里的全部套件，供「发起测试任务」页面的下拉选择
// 和套件管理页面的列表展示。
func (cfg *Config) ListSuites(c *gin.Context) {
	suites, err := cfg.Store.ListSuites()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]SuiteSummary, 0, len(suites))
	for _, suite := range suites {
		summary, err := toSuiteSummary(suite)
		if err != nil {
			// 单条套件定义损坏不该拖垮整个列表接口，跳过并继续——具体错误
			// 会在真正发起任务、加载这个套件时暴露出来。
			log.Printf("api: 跳过损坏的套件 %q: %v", suite.Name, err)
			continue
		}
		out = append(out, summary)
	}
	c.JSON(http.StatusOK, out)
}

type createSuiteRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateSuite 从空白模板创建一个新套件（0 条用例）。用例定义本身
// （request_template/assertion_type/material_ref 等）需要对被测协议和断言
// 类型有专门知识，不是表单能安全生成的——这里创建出的只是一个结构合法、能
// 被 suitedef.ParseSuite 正常加载的空壳，具体用例仍然要靠人工对照已有套件
// 的 SCHEMA.md 编辑补齐（目前没有在线编辑套件用例的 UI）。
func (cfg *Config) CreateSuite(c *gin.Context) {
	var req createSuiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !suiteNamePattern.MatchString(req.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("套件名 %q 不合法：只能包含字母、数字、'_'、'-'", req.Name)})
		return
	}
	if _, err := cfg.Store.GetSuiteByName(req.Name); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("套件 %q 已存在", req.Name)})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	definitionJSON, err := blankSuiteDefinitionJSON(req.Name, req.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	created, err := cfg.Store.CreateSuite(model.Suite{
		Name:           req.Name,
		DefinitionJSON: definitionJSON,
		HasMaterials:   false,
		CreatedAt:      nowRFC3339(),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	summary, err := toSuiteSummary(created)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, summary)
}

func blankSuiteDefinitionJSON(name, description string) (string, error) {
	suiteDoc := map[string]any{
		"suite_id":      name,
		"suite_version": "0.1.0",
		"description":   description,
		"protocol": map[string]any{
			"base_path":    "/v1/chat/completions",
			"auth_header":  "Authorization: Bearer {{api_key}}",
			"content_type": "application/json",
		},
		"defaults": map[string]any{
			"timeout_seconds":            30,
			"category_timeout_overrides": map[string]any{},
		},
		"fixtures": map[string]any{
			"tools":   map[string]any{},
			"prompts": map[string]any{},
		},
		"materials_manifest": "",
		"cases":               []any{},
	}
	out, err := json.MarshalIndent(suiteDoc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化套件骨架失败: %w", err)
	}
	return string(out), nil
}

type cloneSuiteRequest struct {
	SourceSuiteID string `json:"source_suite_id"`
	NewName       string `json:"new_name"`
}

// CloneSuite 复制一个已有套件的定义 + 素材文件到一个新套件，并把拷贝里所有
// 引用旧套件名的地方（definition_json 的 suite_id 字段、素材清单的
// suite_id 字段、每条素材 hosting.frozen_url_path 里的 /materials/<old>/v1/
// 前缀）都改写成新套件名——这几处不改的话，克隆出来的套件能被正常加载，但
// 一旦真正发起多模态用例，素材 URL 还是指向旧套件的托管路径，是个很隐蔽的
// 错误。对应 suites/kimi-k3/suite.v1.json 描述里"作为默认测试套件种子数
// 据，新供应商可克隆本文件"这句设计意图。
func (cfg *Config) CloneSuite(c *gin.Context) {
	var req cloneSuiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !suiteNamePattern.MatchString(req.NewName) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("套件名 %q 不合法：只能包含字母、数字、'_'、'-'", req.NewName)})
		return
	}

	source, err := cfg.Store.GetSuiteByName(req.SourceSuiteID)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("源套件 %q 不存在", req.SourceSuiteID)})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if source.Name == req.NewName {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new_name 不能和源套件名相同"})
		return
	}
	if _, err := cfg.Store.GetSuiteByName(req.NewName); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("套件 %q 已存在", req.NewName)})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	newDefinitionJSON, err := rewriteSuiteIdentityJSON(source.DefinitionJSON, req.NewName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if source.HasMaterials {
		srcDir := filepath.Join(cfg.MaterialsRoot, source.Name, "materials")
		dstDir := filepath.Join(cfg.MaterialsRoot, req.NewName, "materials")
		if _, err := os.Stat(srcDir); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("套件 %q 标记有素材，但素材目录 %s 不存在: %v", source.Name, srcDir, err)})
			return
		}
		if err := os.CopyFS(dstDir, os.DirFS(srcDir)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "复制素材目录失败: " + err.Error()})
			return
		}
		if err := rewriteMaterialsIdentity(filepath.Join(dstDir, "manifest.json"), source.Name, req.NewName); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	created, err := cfg.Store.CreateSuite(model.Suite{
		Name:           req.NewName,
		DefinitionJSON: newDefinitionJSON,
		HasMaterials:   source.HasMaterials,
		CreatedAt:      nowRFC3339(),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	summary, err := toSuiteSummary(created)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, summary)
}

// rewriteSuiteIdentityJSON 把套件定义 JSON 里的 suite_id 字段改成
// newName，并清空 materials_manifest 字段——DB 套件的素材清单固定按
// MaterialsRoot/<name>/materials/manifest.json 这个约定去找（见
// loadSuiteByID），不再依赖这个字段里的相对路径，留着旧值只会误导人。用
// map[string]any 而不是 suitedef.Suite 结构体读写，是因为 Suite 结构体没
// 有声明 description/design_doc 这些字段——按结构体重新序列化会把这些字段
// 悄悄丢掉。
func rewriteSuiteIdentityJSON(raw, newName string) (string, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "", fmt.Errorf("解析套件定义失败: %w", err)
	}
	doc["suite_id"] = newName
	doc["materials_manifest"] = ""

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化套件定义失败: %w", err)
	}
	return string(out), nil
}

// rewriteMaterialsIdentity 就地改写素材清单里的 suite_id 字段，以及每条
// 素材 hosting.frozen_url_path 里指向旧套件名的 URL 前缀。
func rewriteMaterialsIdentity(path, oldName, newName string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取素材清单失败: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("解析素材清单失败: %w", err)
	}
	doc["suite_id"] = newName

	oldURLPrefix := "/materials/" + oldName + "/"
	newURLPrefix := "/materials/" + newName + "/"
	if materials, ok := doc["materials"].([]any); ok {
		for _, item := range materials {
			mat, ok := item.(map[string]any)
			if !ok {
				continue
			}
			hosting, ok := mat["hosting"].(map[string]any)
			if !ok {
				continue
			}
			if u, ok := hosting["frozen_url_path"].(string); ok {
				if rest, cut := strings.CutPrefix(u, oldURLPrefix); cut {
					hosting["frozen_url_path"] = newURLPrefix + rest
				}
			}
		}
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化素材清单失败: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}

// loadSuiteByID 按 TestRun.suite_id 加载套件定义 + 素材清单。优先查数据库
// （suite_id 的第一个 "/" 之前的部分当作套件名——新格式就是纯名字没有
// "/"，旧格式是 "<name>/suite.v1.json"，两种都能正确取出名字）；数据库查
// 不到时回退到按 SuitesRoot 解析文件路径，覆盖迁移前创建、suite_id 还是
// 完整文件路径的历史 TestRun。
func (cfg *Config) loadSuiteByID(suiteID string) (*suitedef.Suite, *suitedef.MaterialsManifest, error) {
	name, _, _ := strings.Cut(suiteID, "/")

	row, err := cfg.Store.GetSuiteByName(name)
	if err == nil {
		suite, perr := suitedef.ParseSuite([]byte(row.DefinitionJSON))
		if perr != nil {
			return nil, nil, fmt.Errorf("解析套件 %q 的定义失败: %w", name, perr)
		}
		var materials *suitedef.MaterialsManifest
		if row.HasMaterials {
			manifestPath := filepath.Join(cfg.MaterialsRoot, row.Name, "materials", "manifest.json")
			materials, err = suitedef.LoadMaterialsManifest(manifestPath)
			if err != nil {
				return nil, nil, fmt.Errorf("加载套件 %q 的素材清单失败: %w", name, err)
			}
		}
		return suite, materials, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, nil, err
	}

	suitePath, ferr := safeJoinResolved(cfg.SuitesRoot, suiteID)
	if ferr != nil {
		return nil, nil, fmt.Errorf("非法的 suite_id: %w", ferr)
	}
	suite, ferr := suitedef.LoadSuite(suitePath)
	if ferr != nil {
		return nil, nil, ferr
	}
	materials, ferr := suitedef.LoadMaterialsManifestForSuite(cfg.RepoRoot, suite)
	if ferr != nil {
		return nil, nil, ferr
	}
	return suite, materials, nil
}

// SeedSuitesFromDisk 把 suitesRoot 下 git 跟踪的套件（<name>/suite.vN.json）
// 同步进数据库：定义 JSON 覆盖式 upsert（见 Store.UpsertSuiteFromDisk 的
// 注释），有素材清单的话把整个 materials/ 目录覆盖拷贝进
// materialsRoot/<name>/materials。单个套件损坏、拷贝失败只记日志跳过，不
// 中断其余套件的同步——这是 api-server 启动时的最佳努力操作，不应该因为一
// 个套件的问题就让整个服务起不来。
func SeedSuitesFromDisk(st *store.Store, suitesRoot, materialsRoot string) error {
	entries, err := os.ReadDir(suitesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取套件种子目录 %s 失败: %w", suitesRoot, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := filepath.Join(suitesRoot, name)
		files, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("api: 读取套件目录 %s 失败，跳过: %v", dir, err)
			continue
		}
		for _, f := range files {
			if f.IsDir() || !suiteFileNamePattern.MatchString(f.Name()) {
				continue
			}
			suitePath := filepath.Join(dir, f.Name())
			raw, err := os.ReadFile(suitePath)
			if err != nil {
				log.Printf("api: 读取套件文件 %s 失败，跳过: %v", suitePath, err)
				continue
			}
			if _, err := suitedef.ParseSuite(raw); err != nil {
				log.Printf("api: 解析套件文件 %s 失败，跳过: %v", suitePath, err)
				continue
			}

			hasMaterials := false
			materialsSrcDir := filepath.Join(dir, "materials")
			if _, err := os.Stat(filepath.Join(materialsSrcDir, "manifest.json")); err == nil {
				hasMaterials = true
				dstDir := filepath.Join(materialsRoot, name, "materials")
				if err := os.RemoveAll(dstDir); err != nil {
					log.Printf("api: 清理套件 %s 的旧素材目录 %s 失败，跳过: %v", name, dstDir, err)
					continue
				}
				if err := os.CopyFS(dstDir, os.DirFS(materialsSrcDir)); err != nil {
					log.Printf("api: 同步套件 %s 的素材文件失败，跳过: %v", name, err)
					continue
				}
			}

			if _, err := st.UpsertSuiteFromDisk(model.Suite{
				Name:           name,
				DefinitionJSON: string(raw),
				HasMaterials:   hasMaterials,
				CreatedAt:      nowRFC3339(),
			}); err != nil {
				log.Printf("api: 导入套件 %s 到数据库失败: %v", name, err)
			}
		}
	}
	return nil
}
