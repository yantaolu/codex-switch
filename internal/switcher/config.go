package switcher

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

var routeKeys = map[string]bool{"model": true, "model_provider": true, "forced_login_method": true, "forced_chatgpt_workspace_id": true, "cli_auth_credentials_store": true, "openai_base_url": true, "chatgpt_base_url": true, "model_catalog_json": true, "profile": true, "review_model": true, "disable_response_storage": true, "network_access": true, "windows_wsl_setup_acknowledged": true}

const providerID = "codex_switch"

func ParseConfig(b []byte) (map[string]any, error) {
	d := map[string]any{}
	if e := toml.Unmarshal(b, &d); e != nil {
		return nil, errors.New("config.toml 语法无效")
	}
	return d, nil
}
func Route(b []byte) (map[string]any, error) {
	d, e := ParseConfig(b)
	if e != nil {
		return nil, e
	}
	r := map[string]any{}
	for k := range routeKeys {
		if v, ok := d[k]; ok {
			r[k] = v
		}
	}
	if features, ok := d["features"].(map[string]any); ok {
		f := map[string]any{}
		for k, v := range features {
			if accountFeatures[k] {
				f[k] = v
			}
		}
		r["features"] = f
	}
	return r, nil
}

// Parser ranges retain comments and all unrelated bytes, including multiline TOML strings.
func PatchConfig(b []byte, route map[string]any, p *Provider, helper, storeDir, alias string, homes ...string) ([]byte, error) {
	original, e := ParseConfig(b)
	if e != nil {
		return nil, e
	}
	if original["profile"] != nil {
		return nil, errors.New("检测到全局 profile 覆盖，请先移除后再切换")
	}
	// A dotted top-level definition of a managed field cannot be safely projected.
	for k := range routeKeys {
		if v, ok := original[k]; ok {
			typ := "string"
			if t, exists := accountRoot[k]; exists {
				typ = t
			}
			if !validOption(v, typ) {
				return nil, fmt.Errorf("路由字段 %s 类型无效", k)
			}
		}
	}
	var parser unstable.Parser
	parser.Reset(b)
	type span struct{ start, end int }
	var spans []span
	inOwned := false
	sectionStart := -1
	for parser.NextExpression() {
		n := parser.Expression()
		offset := int(n.Raw.Offset)
		switch n.Kind {
		case unstable.Table, unstable.ArrayTable:
			offset = int(n.Child().Raw.Offset)
			offset = bytes.LastIndexByte(b[:offset], '\n') + 1
			if inOwned {
				spans = append(spans, span{sectionStart, offset})
			}
			parts := []string{}
			for it := n.Key(); it.Next(); {
				parts = append(parts, string(it.Node().Data))
			}
			inOwned = len(parts) >= 2 && parts[0] == "model_providers" && parts[1] == providerID
			if inOwned {
				sectionStart = offset
			}
		case unstable.KeyValue:
			// Before any table, only top-level switch-owned keys are patched.
			parts := []string{}
			for it := n.Key(); it.Next(); {
				parts = append(parts, string(it.Node().Data))
			}
			if len(parts) == 1 && routeKeys[parts[0]] && sectionStart == -1 {
				start := offset
				end := int(n.Raw.Offset + n.Raw.Length)
				if end < start {
					return nil, errors.New("无法确定配置字段范围")
				}
				start, end = ownedAssignmentSpan(b, start, end)
				spans = append(spans, span{start, end})
			}
		}
		if n.Kind == unstable.Table || n.Kind == unstable.ArrayTable {
			if sectionStart == -1 {
				sectionStart = -2
			}
		}
	}
	if parser.Error() != nil {
		return nil, errors.New("配置解析失败")
	}
	if inOwned {
		spans = append(spans, span{sectionStart, len(b)})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var out strings.Builder
	cursor := 0
	for _, s := range spans {
		if s.start < cursor || s.end > len(b) {
			return nil, errors.New("配置字段范围重叠")
		}
		out.Write(b[cursor:s.start])
		cursor = s.end
	}
	out.Write(b[cursor:])
	r := map[string]any{}
	for k, v := range route {
		if routeKeys[k] && k != "profile" {
			r[k] = v
		}
	}
	r["cli_auth_credentials_store"] = "file"
	if p == nil {
		r["model_provider"] = "openai"
		r["forced_login_method"] = "chatgpt"
		delete(r, "openai_base_url")
		delete(r, "chatgpt_base_url")
	} else {
		r["model_provider"] = providerID
		r["model"] = p.Model
		r["forced_login_method"] = "api"
		delete(r, "forced_chatgpt_workspace_id")
		delete(r, "openai_base_url")
		delete(r, "chatgpt_base_url")
		delete(r, "model_catalog_json")
		if route["model_catalog_json"] != nil {
			r["model_catalog_json"] = route["model_catalog_json"]
		}
	}
	prefix, e := toml.Marshal(r)
	if e != nil {
		return nil, e
	}
	result := append(append(prefix, '\n'), bytes.TrimLeft([]byte(out.String()), "\r\n")...)
	if p != nil {
		args := []string{"--store", storeDir}
		if len(homes) > 0 {
			args = append(args, "--home", homes[0])
		}
		args = append(args, "credential", alias)
		provider := map[string]any{"name": alias, "base_url": p.BaseURL, "wire_api": "responses", "http_headers": p.Headers}
		for k, v := range p.ProviderOptions {
			provider[k] = v
		}
		if p.CatalogMode == "remote" {
			provider["model_catalog_url"] = p.CatalogURL
		}
		if p.AuthMode == "bearer" {
			provider["experimental_bearer_token"] = p.APIKey
			provider["requires_openai_auth"] = false
		} else {
			provider["auth"] = map[string]any{"command": helper, "args": args, "timeout_ms": 10000, "refresh_interval_ms": 0}
		}
		t := map[string]any{"model_providers": map[string]any{providerID: provider}}
		table, err := toml.Marshal(t)
		if err != nil {
			return nil, err
		}
		// The parent may already be explicitly declared in preserved user config.
		// Child tables implicitly create it, so avoid a duplicate parent declaration.
		table = bytes.TrimPrefix(table, []byte("[model_providers]\n"))
		result = append(bytes.TrimRight(result, "\r\n"), '\n', '\n')
		result = append(result, table...)
	}
	result, e = patchFeatures(result, route)
	if e != nil {
		return nil, e
	}
	result = normalizeEmptyLines(result)
	after, e := ParseConfig(result)
	if e != nil {
		return nil, e
	}
	// Enforce semantic preservation of every unrelated field.
	for k, v := range original {
		if routeKeys[k] || k == "model_providers" {
			continue
		}
		if k == "features" {
			beforeFeatures, _ := v.(map[string]any)
			afterFeatures, _ := after[k].(map[string]any)
			for key, val := range beforeFeatures {
				if !accountFeatures[key] && !reflect.DeepEqual(val, afterFeatures[key]) {
					return nil, errors.New("公共 features 配置未能保留")
				}
			}
			continue
		}
		if !reflect.DeepEqual(v, after[k]) {
			return nil, fmt.Errorf("公共配置 %s 未能保留", k)
		}
	}
	if ps, ok := original["model_providers"].(map[string]any); ok {
		for k, v := range ps {
			if k == providerID {
				continue
			}
			ap, _ := after["model_providers"].(map[string]any)
			if !reflect.DeepEqual(v, ap[k]) {
				return nil, errors.New("其他 provider 配置未能保留")
			}
		}
	}
	return result, nil
}
func ValidateProvider(p Provider) error {
	if err := ValidateModels(Models(p), p.Model); err != nil {
		return err
	}
	if p.AuthMode != "" && p.AuthMode != "bearer" {
		return errors.New("未知认证模式")
	}
	if p.CatalogMode != "" && p.CatalogMode != "local" && p.CatalogMode != "remote" {
		return errors.New("未知模型目录模式")
	}
	if p.CatalogMode == "remote" {
		if !validEndpoint(p.CatalogURL) {
			return errors.New("远程模型目录须为有效的 HTTP/HTTPS 地址")
		}
	}
	for k, v := range p.ProviderOptions {
		typ, ok := providerExtra[k]
		if !ok || !validOption(v, typ) {
			return errors.New("不支持的 provider 配置字段或类型")
		}
	}
	for k, v := range p.Options {
		if k == "features" {
			f, ok := v.(map[string]any)
			if !ok {
				return errors.New("features 类型无效")
			}
			for key, val := range f {
				if !accountFeatures[key] || !validOption(val, "bool") {
					return errors.New("不支持的 feature 字段或类型")
				}
			}
			continue
		}
		typ, ok := accountRoot[k]
		if !ok || !validOption(v, typ) {
			return errors.New("不支持的账户配置字段或类型")
		}
	}
	if !validEndpoint(p.BaseURL) {
		return errors.New("接口须为有效的 HTTP/HTTPS 地址")
	}
	if strings.TrimSpace(p.Model) == "" || strings.TrimSpace(p.APIKey) == "" || strings.ContainsAny(p.APIKey, "\r\n") {
		return errors.New("模型和 API key 必填，key 不能含换行")
	}
	for k, v := range p.Headers {
		if !regexp.MustCompile(`^[!#$%&'*+.^_`+"`"+`|~0-9A-Za-z-]+$`).MatchString(k) || strings.ContainsAny(v, "\r\n") || strings.EqualFold(k, "Authorization") {
			return errors.New("请求头无效；Authorization 由认证管理器提供")
		}
	}
	return nil
}

func validEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "http" || u.Scheme == "https")
}
func AuthIdentity(raw []byte) (string, error) {
	var d struct {
		Tokens struct {
			AccountID string `json:"account_id"`
			Access    string `json:"access_token"`
			Refresh   string `json:"refresh_token"`
			ID        string `json:"id_token"`
		} `json:"tokens"`
		APIKey string `json:"OPENAI_API_KEY"`
	}
	if json.Unmarshal(raw, &d) != nil || d.APIKey != "" || d.Tokens.AccountID == "" || d.Tokens.Access == "" || d.Tokens.Refresh == "" {
		return "", errors.New("不是完整的 ChatGPT 登录认证")
	}
	sub := ""
	for _, token := range []string{d.Tokens.ID, d.Tokens.Access} {
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			continue
		}
		b, e := base64.RawURLEncoding.DecodeString(parts[1])
		if e != nil {
			continue
		}
		var claims map[string]any
		if json.Unmarshal(b, &claims) == nil {
			if v, ok := claims["sub"].(string); ok {
				sub = v
				break
			}
		}
	}
	if sub == "" {
		return "", errors.New("认证缺少用户身份，无法安全区分账户")
	}
	return d.Tokens.AccountID + ":" + sub, nil
}
