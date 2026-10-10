package switcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int    `json:"context_window,omitempty"`
}
type ModelUpdate struct {
	Provider    *Provider
	Models      []Model
	Default     string
	CatalogMode string
}

func Models(p Provider) []Model {
	if len(p.Models) == 0 {
		return []Model{{ID: p.Model}}
	}
	return append([]Model(nil), p.Models...)
}
func ValidateModels(models []Model, def string) error {
	if len(models) == 0 || len(models) > 200 {
		return errors.New("模型列表须包含 1–200 个模型")
	}
	seen := map[string]bool{}
	hasDefault := false
	for _, m := range models {
		if strings.TrimSpace(m.ID) != m.ID || m.ID == "" || len(m.ID) > 200 || strings.ContainsAny(m.ID, "\r\n\x1b\t") {
			return errors.New("模型 ID 无效")
		}
		if strings.ContainsAny(m.Name, "\r\n\x1b") {
			return errors.New("模型显示名称无效")
		}
		if seen[m.ID] {
			return errors.New("模型 ID 不能重复")
		}
		seen[m.ID] = true
		if m.ID == def {
			hasDefault = true
		}
		if m.ContextWindow != 0 && (m.ContextWindow < 1024 || m.ContextWindow > 10000000) {
			return errors.New("上下文窗口须为 1024–10000000，或留空")
		}
	}
	if !hasDefault {
		return errors.New("默认模型必须在模型列表中")
	}
	return nil
}
func Catalog(p Provider) ([]byte, error) {
	models := Models(p)
	if err := ValidateModels(models, p.Model); err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for i, m := range models {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		priority := i + 1
		if m.ID == p.Model {
			priority = 0
		}
		row := map[string]any{
			"slug": m.ID, "display_name": name, "description": "Configured third-party Responses model",
			"default_reasoning_level": "none", "supported_reasoning_levels": []any{}, "shell_type": "shell_command", "visibility": "list", "supported_in_api": true, "priority": priority,
			"support_verbosity": false, "supports_reasoning_summary_parameter": false, "default_reasoning_summary": "none",
			"truncation_policy": map[string]any{"mode": "tokens", "limit": 10000}, "experimental_supported_tools": []string{}, "input_modalities": []string{"text"},
			"base_instructions": "You are a coding assistant. Follow the user's instructions, inspect relevant files before changing code, and verify your work. Use the available tools to complete authorized tasks. Keep credentials private.",
			"model_messages":    map[string]any{"instructions_template": "You are a coding assistant. Follow the user's instructions, inspect relevant files before changing code, and verify your work. Use the available tools to complete authorized tasks. Keep credentials private."},
		}
		if m.ContextWindow > 0 {
			row["context_window"] = m.ContextWindow
		}
		rows = append(rows, row)
	}
	return json.MarshalIndent(map[string]any{"models": rows}, "", "  ")
}
func (e Engine) catalog(p Provider) (string, error) {
	b, err := Catalog(p)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(e.Store.Dir, "catalogs")
	if err = privateDir(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, catalogFilename(b))
	if existing, err := readSafe(path); err == nil && string(existing) == string(b) {
		return path, nil
	}
	if err = atomicWrite(path, b); err != nil {
		return "", err
	}
	return path, nil
}
func (e Engine) UpdateModels(name string, models []Model, def string) error {
	if err := ValidateModels(models, def); err != nil {
		return err
	}
	// Active updates use the same transaction and recovery path as account switching.
	return e.switchProfile(name, &ModelUpdate{Models: models, Default: def})
}

func (e Engine) UseRemoteCatalog(name string) error {
	return e.switchProfile(name, &ModelUpdate{CatalogMode: "remote"})
}

func catalogFilename(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]) + ".json"
}
