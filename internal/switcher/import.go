package switcher

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var accountRoot = map[string]string{
	"review_model": "string",
}

// These top-level fields are ignored for all macOS accounts. Keep them owned
// by the config patcher so old live configurations can be cleaned on switching.
var ignoredAccountRoot = map[string]string{
	"disable_response_storage":       "当前配置不支持该顶层字段",
	"network_access":                 "该顶层字段无效，保留已有沙箱联网设置",
	"windows_wsl_setup_acknowledged": "macOS 不使用 Windows 引导状态",
}

// Copy only when cleanup is needed; callers may still hold the original map
// for a failed save or rollback. Nested features and other values are untouched.
func normalizedAccountOptions(options map[string]any) map[string]any {
	for key := range ignoredAccountRoot {
		if _, exists := options[key]; exists {
			clean := make(map[string]any, len(options))
			for key, value := range options {
				if _, ignored := ignoredAccountRoot[key]; !ignored {
					clean[key] = value
				}
			}
			return clean
		}
	}
	return options
}

var accountFeatures = map[string]bool{"api_key_model_discovery": true, "goals": true, "image_generation": true}
var providerExtra = map[string]string{
	"supports_websockets": "bool", "supports_standalone_web_search": "bool",
	"request_max_retries": "int", "stream_max_retries": "int", "stream_idle_timeout_ms": "int", "websocket_connect_timeout_ms": "int",
}

// ImportTOML deliberately does not import commands, hooks, MCP servers or paths.
// All diagnostics name fields only; source text may contain credentials.
func ImportTOML(raw []byte) (Provider, []string, error) {
	var p Provider
	if len(raw) > 1024*1024 {
		return p, nil, errors.New("导入内容不能超过 1 MiB")
	}
	d, err := ParseConfig(raw)
	if err != nil {
		return p, nil, err
	}
	id, _ := d["model_provider"].(string)
	if id == "" {
		return p, nil, errors.New("缺少 model_provider")
	}
	providers, _ := d["model_providers"].(map[string]any)
	selected, ok := providers[id].(map[string]any)
	if !ok {
		return p, nil, errors.New("找不到 model_provider 对应的配置表")
	}
	p.BaseURL, _ = selected["base_url"].(string)
	p.Model, _ = d["model"].(string)
	p.APIKey, _ = selected["experimental_bearer_token"].(string)
	p.AuthMode = "bearer"
	p.CatalogURL, _ = selected["model_catalog_url"].(string)
	if p.CatalogURL != "" {
		p.CatalogMode = "remote"
	}
	if selected["auth"] != nil || selected["env_key"] != nil {
		return p, nil, errors.New("导入不执行外部认证命令或读取环境变量；请使用 experimental_bearer_token")
	}
	if v, ok := selected["wire_api"]; ok && v != "responses" {
		return p, nil, errors.New("目前仅支持 Responses API")
	}
	if v, ok := selected["requires_openai_auth"]; ok && v != false {
		return p, nil, errors.New("第三方导入要求 requires_openai_auth = false")
	}
	p.Headers = map[string]string{}
	if h, ok := selected["http_headers"]; ok {
		table, ok := h.(map[string]any)
		if !ok {
			return p, nil, errors.New("http_headers 必须是字符串映射")
		}
		for k, v := range table {
			s, ok := v.(string)
			if !ok {
				return p, nil, errors.New("请求头值必须是字符串")
			}
			p.Headers[k] = s
		}
	}
	warnings := []string{}
	p.Options = map[string]any{}
	for k, v := range d {
		if reason, ignored := ignoredAccountRoot[k]; ignored {
			warnings = append(warnings, k+"：已过滤（"+reason+"）")
		} else if typ, ok := accountRoot[k]; ok {
			if !validOption(v, typ) {
				return p, nil, fmt.Errorf("字段 %s 类型无效", k)
			}
			p.Options[k] = v
		} else if k != "model" && k != "model_provider" && k != "model_providers" && k != "features" {
			warnings = append(warnings, k+"：未导入")
		}
	}
	if v, ok := d["features"]; ok {
		f, ok := v.(map[string]any)
		if !ok {
			return p, nil, errors.New("features 必须是配置表")
		}
		selectedFeatures := map[string]any{}
		for k, v := range f {
			if accountFeatures[k] {
				if !validOption(v, "bool") {
					return p, nil, fmt.Errorf("features.%s 必须是布尔值", k)
				}
				selectedFeatures[k] = v
			} else {
				warnings = append(warnings, "features."+k+"：未导入")
			}
		}
		p.Options["features"] = selectedFeatures
	}
	p.ProviderOptions = map[string]any{}
	for k, v := range selected {
		if typ, ok := providerExtra[k]; ok {
			if !validOption(v, typ) {
				return p, nil, fmt.Errorf("provider 字段 %s 类型无效", k)
			}
			p.ProviderOptions[k] = v
		} else if k != "name" && k != "base_url" && k != "model_catalog_url" && k != "wire_api" && k != "requires_openai_auth" && k != "experimental_bearer_token" && k != "http_headers" {
			warnings = append(warnings, "provider."+k+"：未导入")
		}
	}
	for other := range providers {
		if other != id {
			warnings = append(warnings, "其他 provider：未导入")
		}
	}
	if p.CatalogMode == "remote" {
		f, _ := p.Options["features"].(map[string]any)
		if f["api_key_model_discovery"] != true {
			warnings = append(warnings, "features.api_key_model_discovery：未启用，远程目录可能不会被读取")
		}
	}
	sort.Strings(warnings)
	if err = ValidateProvider(p); err != nil {
		return Provider{}, nil, err
	}
	return p, warnings, nil
}

func validOption(v any, typ string) bool {
	switch typ {
	case "string":
		s, ok := v.(string)
		return ok && !strings.ContainsAny(s, "\r\n\x1b")
	case "bool":
		_, ok := v.(bool)
		return ok
	case "int":
		switch n := v.(type) {
		case int64:
			return n >= 0
		case float64:
			return n >= 0 && n == float64(int64(n))
		case int:
			return n >= 0
		}
	}
	return false
}
