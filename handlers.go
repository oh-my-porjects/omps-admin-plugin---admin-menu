package main

import (
	"encoding/json"
	"io"
	"net/http"
)

// 请求体协议：admin-server 推 spec 时发的 payload
type writeReq struct {
	Spec          json.RawMessage `json:"spec"`            // 完整 admin spec JSON
	PromptVer     int             `json:"prompt_version"`  // 提示词版本号
	ModuleVer     string          `json:"module_version"`  // 当时模块版本
	GeneratedBy   string          `json:"generated_by"`    // 生成者标识（auto-on-deploy / user:xxx）
	ReqID         int64           `json:"req_id"`          // 关联需求 id
	DeployID      int64           `json:"deploy_id"`       // 关联部署 id
}

// handleSpecWrite POST /api/admin-meta/specs/{module}
func (p *AdminMenuPlugin) handleSpecWrite(w http.ResponseWriter, r *http.Request) {
	module := r.PathValue("module")
	if module == "" {
		writeJSON(w, http.StatusBadRequest, gin1{"code": 1, "message": "module 不能为空"})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, gin1{"code": 1, "message": "读 body 失败: " + err.Error()})
		return
	}
	defer r.Body.Close()

	var req writeReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, gin1{"code": 1, "message": "JSON 解析失败: " + err.Error()})
		return
	}
	if len(req.Spec) == 0 {
		writeJSON(w, http.StatusBadRequest, gin1{"code": 1, "message": "spec 不能为空"})
		return
	}

	version, err := upsertSpec(p.db, SpecRow{
		Module:      module,
		SpecJSON:    req.Spec,
		PromptVer:   req.PromptVer,
		ModuleVer:   req.ModuleVer,
		GeneratedBy: req.GeneratedBy,
		ReqID:       req.ReqID,
		DeployID:    req.DeployID,
	})
	if err != nil {
		p.logger.Error("admin-meta 写入 spec 失败", "module", module, "err", err)
		writeJSON(w, http.StatusInternalServerError, gin1{"code": 1, "message": "写入失败: " + err.Error()})
		return
	}
	p.logger.Info("admin-meta 写入 spec 成功", "module", module, "version", version, "by", req.GeneratedBy)
	writeJSON(w, http.StatusOK, gin1{"code": 0, "data": gin1{"module": module, "version": version}})
}

// handleSpecDelete DELETE /api/admin-meta/specs/{module}
func (p *AdminMenuPlugin) handleSpecDelete(w http.ResponseWriter, r *http.Request) {
	module := r.PathValue("module")
	if module == "" {
		writeJSON(w, http.StatusBadRequest, gin1{"code": 1, "message": "module 不能为空"})
		return
	}
	if err := deleteSpec(p.db, module); err != nil {
		writeJSON(w, http.StatusInternalServerError, gin1{"code": 1, "message": err.Error()})
		return
	}
	p.logger.Info("admin-meta 删除 spec", "module", module)
	writeJSON(w, http.StatusOK, gin1{"code": 0})
}

// handleSpecGet GET /api/admin-meta/specs/{module}
//
// data 字段直接是平铺的 spec 对象（含 menus / views / roles 等顶层字段），
// 跟前端 useSpec hook 的 Spec 类型 1:1 对齐：const spec = data.data as Spec
// 模块没记录时 data 为 null（与原 runtime 缓存接口语义一致）
func (p *AdminMenuPlugin) handleSpecGet(w http.ResponseWriter, r *http.Request) {
	module := r.PathValue("module")
	row, err := getSpec(p.db, module)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, gin1{"code": 1, "message": err.Error()})
		return
	}
	if row == nil {
		writeJSON(w, http.StatusOK, gin1{"code": 0, "data": nil})
		return
	}
	// 把 spec_json 解出来作为 data 顶层；spec JSON 已经有 module / version / generated_at
	// 等字段（admin-server adminweb.Spec 序列化结果），平铺给前端跟老链路结构兼容
	var specObj map[string]any
	if err := json.Unmarshal(row.SpecJSON, &specObj); err != nil {
		writeJSON(w, http.StatusInternalServerError, gin1{"code": 1, "message": "spec JSON 解析失败: " + err.Error()})
		return
	}
	// 用 plugin 自己存的元数据兜底（admin-server 推送时不一定都填到 spec 内）
	if _, ok := specObj["version"]; !ok {
		specObj["version"] = row.Version
	}
	if _, ok := specObj["prompt_version"]; !ok {
		specObj["prompt_version"] = row.PromptVer
	}
	if _, ok := specObj["module_version"]; !ok {
		specObj["module_version"] = row.ModuleVer
	}
	if _, ok := specObj["module"]; !ok {
		specObj["module"] = row.Module
	}
	writeJSON(w, http.StatusOK, gin1{"code": 0, "data": specObj})
}

// handleSpecList GET /api/admin-meta/specs
//
// 列出所有模块 spec 元数据（含完整 spec_json，调用方可挑用）
func (p *AdminMenuPlugin) handleSpecList(w http.ResponseWriter, r *http.Request) {
	rows, err := listAll(p.db)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, gin1{"code": 1, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, gin1{"code": 0, "data": rows})
}

// handleMenu GET /api/admin-meta/menu
//
// 把所有模块 spec 里的 menus 部分聚合成 menu tree 返回，前端 DynamicShell 直接渲染左侧导航
func (p *AdminMenuPlugin) handleMenu(w http.ResponseWriter, r *http.Request) {
	rows, err := listAll(p.db)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, gin1{"code": 1, "message": err.Error()})
		return
	}
	tree := buildMenuTree(rows)
	writeJSON(w, http.StatusOK, gin1{"code": 0, "data": tree})
}

// gin1 节省字符的 map alias（plugin 内不依赖 gin 包）
type gin1 map[string]any

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
