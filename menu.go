package main

import "encoding/json"

// 菜单聚合：从所有模块的完整 spec JSON 里抽出 menus 字段，按模块分组返回
//
// 输出格式跟原 admin-server 一致，前端 DynamicShell 透明切换：
//
//	{
//	  "version": <int>,
//	  "modules": [
//	    {"module": "users", "menus": [{"key": "list", "title": "用户列表", "icon": "...", "roles": [...]}, ...]},
//	    {"module": "orders", "menus": [...]},
//	  ]
//	}
//
// version 用所有行 max(version) 之和近似（任一变化会递增，前端用来失效缓存）

type menuItem struct {
	Key   string   `json:"key"`
	Title string   `json:"title"`
	Icon  string   `json:"icon,omitempty"`
	Roles []string `json:"roles,omitempty"`
}

type moduleEntry struct {
	Module string     `json:"module"`
	Menus  []menuItem `json:"menus"`
}

type menuTree struct {
	Version int           `json:"version"`
	Modules []moduleEntry `json:"modules"`
}

func buildMenuTree(rows []SpecRow) menuTree {
	out := menuTree{Modules: make([]moduleEntry, 0, len(rows))}
	for _, r := range rows {
		out.Version += r.Version
		menus := extractMenus(r.SpecJSON)
		if len(menus) == 0 {
			continue
		}
		out.Modules = append(out.Modules, moduleEntry{
			Module: r.Module,
			Menus:  menus,
		})
	}
	return out
}

// extractMenus 解析 spec_json 的 menus 数组
//
// spec 完整结构由 admin-server AI 生成，本插件只关心 menus 顶层字段
// 解析失败或字段缺失返回 nil，不报错（让其他模块的菜单照常显示）
func extractMenus(raw json.RawMessage) []menuItem {
	if len(raw) == 0 {
		return nil
	}
	var partial struct {
		Menus []menuItem `json:"menus"`
	}
	if err := json.Unmarshal(raw, &partial); err != nil {
		return nil
	}
	return partial.Menus
}
