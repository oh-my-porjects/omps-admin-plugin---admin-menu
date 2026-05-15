package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SpecRow 表一行
type SpecRow struct {
	Module        string          `json:"module"`
	SpecJSON      json.RawMessage `json:"spec"`
	Version       int             `json:"version"`
	PromptVer     int             `json:"prompt_version"`
	ModuleVer     string          `json:"module_version"`
	GeneratedBy   string          `json:"generated_by"`
	ReqID         int64           `json:"req_id"`
	DeployID      int64           `json:"deploy_id"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// ensureSchema 建表（幂等）+ 老 admin_meta_specs 数据迁移（task/inner_plugin.md §9.4）
//
// 单 (project, env) 内每个 module 一行，本插件不区分 project/env
// 因为一个 runtime 容器只服务一个 (project, env)，spec 也只属于本环境
//
// 数据迁移：admin_meta builtin plugin 下线（§15.1）后老 spec 存在 admin_meta_specs
// 表里，admin-menu 启动时把这些行 COPY 进 admin_menu_specs。COPY 用 ON CONFLICT
// DO NOTHING 保护新 push 的数据不被覆盖；迁移失败不阻塞 Init（老表可能本来不存在）
func ensureSchema(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("admin-menu: DB 未注入")
	}
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS admin_menu_specs (
			module          TEXT PRIMARY KEY,
			spec_json       JSONB NOT NULL,
			version         INT NOT NULL DEFAULT 1,
			prompt_version  INT NOT NULL DEFAULT 0,
			module_version  TEXT NOT NULL DEFAULT '',
			generated_by    TEXT NOT NULL DEFAULT '',
			req_id          BIGINT NOT NULL DEFAULT 0,
			deploy_id       BIGINT NOT NULL DEFAULT 0,
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`
		INSERT INTO admin_menu_specs
			(module, spec_json, version, prompt_version, module_version, generated_by, req_id, deploy_id, updated_at)
		SELECT module, spec_json, version, prompt_version, module_version, generated_by, req_id, deploy_id, updated_at
		FROM admin_meta_specs
		ON CONFLICT (module) DO NOTHING
	`)
	return nil
}

// upsertSpec 覆盖式写入 spec
//
// 每次写入 version 自增（取已有 +1，新行 1）
func upsertSpec(db *sql.DB, row SpecRow) (int, error) {
	var newVersion int
	err := db.QueryRow(`
		INSERT INTO admin_menu_specs(module, spec_json, version, prompt_version, module_version, generated_by, req_id, deploy_id, updated_at)
		VALUES ($1, $2, 1, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (module) DO UPDATE SET
			spec_json = EXCLUDED.spec_json,
			version = admin_menu_specs.version + 1,
			prompt_version = EXCLUDED.prompt_version,
			module_version = EXCLUDED.module_version,
			generated_by = EXCLUDED.generated_by,
			req_id = EXCLUDED.req_id,
			deploy_id = EXCLUDED.deploy_id,
			updated_at = NOW()
		RETURNING version
	`, row.Module, []byte(row.SpecJSON), row.PromptVer, row.ModuleVer, row.GeneratedBy, row.ReqID, row.DeployID).Scan(&newVersion)
	return newVersion, err
}

// deleteSpec 删除某模块 spec（模块卸载时调）
func deleteSpec(db *sql.DB, module string) error {
	_, err := db.Exec(`DELETE FROM admin_menu_specs WHERE module = $1`, module)
	return err
}

// getSpec 拿单模块完整 spec
func getSpec(db *sql.DB, module string) (*SpecRow, error) {
	var r SpecRow
	var specBytes []byte
	err := db.QueryRow(`
		SELECT module, spec_json, version, prompt_version, module_version, generated_by, req_id, deploy_id, updated_at
		FROM admin_menu_specs WHERE module = $1
	`, module).Scan(&r.Module, &specBytes, &r.Version, &r.PromptVer, &r.ModuleVer, &r.GeneratedBy, &r.ReqID, &r.DeployID, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.SpecJSON = specBytes
	return &r, nil
}

// listAll 列出全部 spec（菜单聚合用，按模块名排序）
func listAll(db *sql.DB) ([]SpecRow, error) {
	rows, err := db.Query(`
		SELECT module, spec_json, version, prompt_version, module_version, generated_by, req_id, deploy_id, updated_at
		FROM admin_menu_specs ORDER BY module
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpecRow
	for rows.Next() {
		var r SpecRow
		var specBytes []byte
		if err := rows.Scan(&r.Module, &specBytes, &r.Version, &r.PromptVer, &r.ModuleVer, &r.GeneratedBy, &r.ReqID, &r.DeployID, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.SpecJSON = specBytes
		out = append(out, r)
	}
	return out, rows.Err()
}
