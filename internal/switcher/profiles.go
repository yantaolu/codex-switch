package switcher

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

func ValidateRemark(remark string) error {
	if !utf8.ValidString(remark) || strings.TrimSpace(remark) != remark || remark == "" || utf8.RuneCountInString(remark) > 64 {
		return errors.New("账户备注须为 1–64 个字符，首尾不能有空格")
	}
	for _, r := range remark {
		if unicode.IsControl(r) {
			return errors.New("账户备注不能包含控制字符")
		}
	}
	return nil
}
func ProfileID(remark string) string {
	if ValidateName(remark) == nil {
		return remark
	}
	sum := sha256.Sum256([]byte(remark))
	return "account-" + hex.EncodeToString(sum[:6])
}
func ProfileLabel(id string, p Profile) string {
	if p.Remark != "" {
		return p.Remark
	}
	return id
}
func ProfileDisplay(id string, p Profile) string {
	label := ProfileLabel(id, p)
	if label != id {
		return label + " [" + id + "]"
	}
	return label
}
func (e Engine) SetRemark(id, remark string) error {
	if err := ValidateRemark(remark); err != nil {
		return err
	}
	unlock, err := e.Store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = e.checkPending(); err != nil {
		return err
	}
	st, err := e.state()
	if err != nil {
		return err
	}
	p, ok := st.Profiles[id]
	if !ok {
		return errors.New("账户不存在")
	}
	p.Remark = remark
	st.Profiles[id] = p
	return e.Store.Save(st)
}
func (e Engine) UpdateProvider(id string, provider Provider) error {
	if err := ValidateProvider(provider); err != nil {
		return err
	}
	return e.switchProfile(id, &ModelUpdate{Provider: &provider})
}

// Editable export contains credentials by design. It is never logged.
func ProviderTOML(p Provider) ([]byte, error) {
	if err := ValidateProvider(p); err != nil {
		return nil, err
	}
	root := map[string]any{"model_provider": providerID, "model": p.Model}
	for key, value := range p.Options {
		root[key] = value
	}
	provider := map[string]any{"name": "codex-switch", "base_url": p.BaseURL, "wire_api": "responses", "requires_openai_auth": false, "experimental_bearer_token": p.APIKey}
	if p.CatalogURL != "" {
		provider["model_catalog_url"] = p.CatalogURL
	}
	if len(p.Headers) > 0 {
		provider["http_headers"] = p.Headers
	}
	for key, value := range p.ProviderOptions {
		provider[key] = value
	}
	root["model_providers"] = map[string]any{providerID: provider}
	return toml.Marshal(root)
}
