package switcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// UsageConfig belongs to a profile, independently of its model connection.
// Generic fields are JSON paths (dot notation or JSON pointer), not executable code.
type UsageConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url,omitempty"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	APIKey  string            `json:"api_key,omitempty"`
	Fields  map[string]string `json:"fields,omitempty"`
	Scale   float64           `json:"scale,omitempty"`
	Unit    string            `json:"unit,omitempty"`
}
type UsageMetric struct {
	Name      string    `json:"name"`
	Remaining *float64  `json:"remaining,omitempty"`
	Used      *float64  `json:"used,omitempty"`
	Total     *float64  `json:"total,omitempty"`
	Unit      string    `json:"unit,omitempty"`
	ResetAt   time.Time `json:"reset_at,omitempty"`
}
type UsageSnapshot struct {
	Plan        string        `json:"plan,omitempty"`
	Status      string        `json:"status,omitempty"`
	ExpiresAt   time.Time     `json:"expires_at,omitempty"`
	Metrics     []UsageMetric `json:"metrics,omitempty"`
	UpdatedAt   time.Time     `json:"updated_at,omitempty"`
	AttemptedAt time.Time     `json:"attempted_at,omitempty"`
	Error       string        `json:"error,omitempty"`
	Fingerprint string        `json:"fingerprint,omitempty"`
}

func ValidateUsage(c UsageConfig) error {
	if c.Type == "off" {
		return nil
	}
	if c.Type != "sub2api" && c.Type != "generic" {
		return errors.New("查询类型须为 sub2api、generic 或 off")
	}
	if c.URL != "" && !validEndpoint(c.URL) {
		return errors.New("查询地址须为 HTTP/HTTPS URL")
	}
	if c.Type == "generic" && c.URL == "" {
		return errors.New("通用模板需要查询地址")
	}
	if c.Method != "" && c.Method != "GET" && c.Method != "POST" {
		return errors.New("查询方法须为 GET 或 POST")
	}
	if math.IsNaN(c.Scale) || math.IsInf(c.Scale, 0) || c.Scale < 0 {
		return errors.New("换算系数须为正数，或留空")
	}
	for k, v := range c.Headers {
		if !validHeader(k, v) {
			return errors.New("查询请求头无效")
		}
	}
	for k := range c.Fields {
		switch k {
		case "remaining", "used", "total", "plan", "unit", "expires_at", "is_valid":
		default:
			return fmt.Errorf("未知查询字段：%s", k)
		}
	}
	if c.Type == "generic" && c.Fields["remaining"] == "" && c.Fields["used"] == "" && c.Fields["total"] == "" && c.Fields["plan"] == "" {
		return errors.New("至少配置一个余额、用量或套餐字段")
	}
	return nil
}
func validHeader(k, v string) bool {
	if k == "" || strings.ContainsAny(v, "\r\n") {
		return false
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}
func DefaultUsage(p Profile, kind string) UsageConfig {
	c := UsageConfig{Type: kind}
	if kind == "generic" {
		base := "https://example.com"
		if p.Provider != nil {
			base = strings.TrimSuffix(p.Provider.BaseURL, "/")
		}
		c.URL = base + "/user/balance"
		c.Headers = map[string]string{"Authorization": "Bearer {{apiKey}}"}
		c.Fields = map[string]string{"remaining": "balance"}
		c.Unit = "USD"
	}
	return c
}
func Sub2APIUsageURL(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path += "/v1"
	}
	u.Path += "/usage"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func (e Engine) UpdateUsage(name string, c UsageConfig) error {
	if err := ValidateUsage(c); err != nil {
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
	p, ok := st.Profiles[name]
	if !ok {
		return errors.New("账户不存在")
	}
	if p.Kind != "provider" {
		return errors.New("官方账户使用内置查询，无需配置模板")
	}
	p.Usage = &c
	st.Profiles[name] = p
	return e.Store.Save(st)
}
func usageFingerprint(p Profile) string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (e Engine) CachedUsage(name string) UsageSnapshot {
	st, err := e.state()
	if err != nil {
		return UsageSnapshot{}
	}
	p, ok := st.Profiles[name]
	if !ok {
		return UsageSnapshot{}
	}
	var cache map[string]UsageSnapshot
	if e.Store.read("usage.enc", &cache) != nil {
		return UsageSnapshot{}
	}
	u := cache[name]
	if u.Fingerprint != usageFingerprint(p) {
		return UsageSnapshot{}
	}
	return u
}
func (e Engine) QueryUsage(ctx context.Context, name string, force bool) (UsageSnapshot, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	unlock, err := e.Store.Lock()
	if err != nil {
		return UsageSnapshot{}, err
	}
	st, err := e.state()
	if err != nil {
		unlock()
		return UsageSnapshot{}, err
	}
	p, ok := st.Profiles[name]
	if !ok {
		unlock()
		return UsageSnapshot{}, errors.New("账户不存在")
	}
	if e.Store.HasJournal() {
		unlock()
		return UsageSnapshot{}, errors.New("请先恢复未完成的切换")
	}
	fingerprint := usageFingerprint(p)
	var cache map[string]UsageSnapshot
	readErr := e.Store.read("usage.enc", &cache)
	if readErr != nil && !os.IsNotExist(readErr) {
		unlock()
		return UsageSnapshot{}, readErr
	}
	old := cache[name]
	if old.Fingerprint != fingerprint {
		old = UsageSnapshot{}
	}
	if !force && !old.AttemptedAt.IsZero() && time.Since(old.AttemptedAt) < 30*time.Second {
		unlock()
		return old, nil
	}
	// Prefer the live file only when it still belongs to the selected profile.
	if p.Kind == "chatgpt" && name == st.Current {
		live, er := readSafe(filepath.Join(e.Home, "auth.json"))
		a, ae := AuthIdentity(live)
		b, be := AuthIdentity(p.Auth)
		if er == nil && ae == nil && be == nil && a == b {
			p.Auth = live
		}
	}
	unlock()
	var u UsageSnapshot
	if p.Kind == "chatgpt" {
		r, ok := e.Runtime.(MacRuntime)
		if !ok {
			err = errors.New("官方查询需要 Codex CLI")
		} else {
			u, err = queryOfficial(ctx, r.Codex, p.Auth)
		}
	} else if p.Provider == nil {
		err = errors.New("账户配置无效")
	} else if p.Usage == nil || p.Usage.Type == "off" {
		err = errors.New("未配置查询；请进入账户详情的额度配置")
	} else {
		u, err = queryProvider(ctx, p)
	}
	if parent.Err() != nil {
		return old, parent.Err()
	}
	now := time.Now()
	if err != nil {
		u = old
		u.Error = err.Error()
	} else {
		u.UpdatedAt = now
	}
	u.AttemptedAt = now
	u.Fingerprint = fingerprint
	// Reload under lock: a query may overlap a switch or another account query.
	unlock, lockErr := e.Store.Lock()
	if lockErr != nil {
		return u, lockErr
	}
	defer unlock()
	latest, loadErr := e.state()
	if loadErr != nil {
		return u, loadErr
	}
	if current, ok := latest.Profiles[name]; !ok || usageFingerprint(current) != fingerprint {
		return u, errors.New("账户配置已变化，请重新查询")
	}
	cache = map[string]UsageSnapshot{}
	readErr = e.Store.read("usage.enc", &cache)
	if readErr != nil && !os.IsNotExist(readErr) {
		return u, readErr
	}
	if cache == nil {
		cache = map[string]UsageSnapshot{}
	}
	for cachedName := range cache {
		if _, exists := latest.Profiles[cachedName]; !exists {
			delete(cache, cachedName)
		}
	}
	cache[name] = u
	if saveErr := e.Store.write("usage.enc", cache); saveErr != nil {
		return u, saveErr
	}
	return u, nil // Query failures are displayed with cached data, never as zero.
}
func queryProvider(ctx context.Context, p Profile) (UsageSnapshot, error) {
	c := *p.Usage
	if err := ValidateUsage(c); err != nil {
		return UsageSnapshot{}, err
	}
	endpoint := c.URL
	if endpoint == "" {
		endpoint = Sub2APIUsageURL(p.Provider.BaseURL)
	}
	key := c.APIKey
	if key == "" {
		key = p.Provider.APIKey
	}
	replace := func(v string) string {
		v = strings.ReplaceAll(v, "{{apiKey}}", key)
		return strings.ReplaceAll(v, "{{baseUrl}}", strings.TrimSuffix(p.Provider.BaseURL, "/"))
	}
	method := c.Method
	if method == "" {
		method = "GET"
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(replace(c.Body)))
	if err != nil {
		return UsageSnapshot{}, errors.New("查询请求无效")
	}
	// Provider image-executor headers are not balance-query credentials.
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if c.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.Headers {
		req.Header.Set(k, replace(v))
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return UsageSnapshot{}, errors.New("查询超时或已取消")
		}
		return UsageSnapshot{}, errors.New("无法连接查询接口")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return UsageSnapshot{}, errors.New("查询鉴权失败，请检查 API Key 或查询权限")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UsageSnapshot{}, fmt.Errorf("查询接口返回 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return UsageSnapshot{}, errors.New("查询响应无法读取或过大")
	}
	var d map[string]any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&d) != nil {
		return UsageSnapshot{}, errors.New("查询接口未返回 JSON 对象")
	}
	if c.Type == "sub2api" {
		return parseSub2API(d)
	}
	return parseGeneric(d, c)
}
func number(v any) *float64 {
	var n float64
	var err error
	switch x := v.(type) {
	case json.Number:
		n, err = x.Float64()
	case float64:
		n = x
	case string:
		n, err = strconv.ParseFloat(x, 64)
	default:
		return nil
	}
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}
	return &n
}
func str(v any) string         { s, _ := v.(string); return s }
func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func parseTime(v any) time.Time {
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	if n := number(v); n != nil {
		if *n >= 1e12 {
			return time.UnixMilli(int64(*n)).UTC()
		}
		return time.Unix(int64(*n), 0).UTC()
	}
	return time.Time{}
}
func amountMetric(name string, m map[string]any, unit string) UsageMetric {
	u := UsageMetric{Name: name, Remaining: number(m["remaining"]), Used: number(m["used"]), Total: number(m["limit"]), Unit: unit, ResetAt: parseTime(m["reset_at"])}
	if u.Remaining == nil && u.Total != nil && u.Used != nil {
		n := math.Max(0, *u.Total-*u.Used)
		u.Remaining = &n
	}
	return u
}
func parseSub2API(d map[string]any) (UsageSnapshot, error) {
	if data := obj(d["data"]); data != nil && d["mode"] == nil {
		d = data
	}
	u := UsageSnapshot{Plan: str(d["planName"]), Status: str(d["status"]), ExpiresAt: parseTime(d["expires_at"])}
	if v, ok := d["isValid"].(bool); ok && !v {
		u.Status = "账户不可用"
	}
	unit := str(d["unit"])
	if unit == "" {
		unit = "USD"
	}
	if b := number(d["balance"]); b != nil {
		u.Metrics = append(u.Metrics, UsageMetric{Name: "钱包余额", Remaining: b, Unit: unit})
	}
	if q := obj(d["quota"]); q != nil {
		u.Metrics = append(u.Metrics, amountMetric("Key 配额", q, unit))
	}
	if sub := obj(d["subscription"]); sub != nil {
		u.ExpiresAt = parseTime(sub["expires_at"])
		for _, period := range []struct{ key, label string }{{"daily", "每日额度"}, {"weekly", "每周额度"}, {"monthly", "每月额度"}} {
			total := number(sub[period.key+"_limit_usd"])
			used := number(sub[period.key+"_usage_usd"])
			if total != nil && *total > 0 {
				u.Metrics = append(u.Metrics, amountMetric(period.label, map[string]any{"limit": *total, "used": valueOrNil(used)}, unit))
			}
		}
	}
	if limits, ok := d["rate_limits"].([]any); ok {
		for _, l := range limits {
			m := obj(l)
			u.Metrics = append(u.Metrics, amountMetric(str(m["window"])+" 配额", m, unit))
		}
	}
	if len(u.Metrics) == 0 {
		if n := number(d["remaining"]); n != nil {
			if *n < 0 && u.Plan != "" {
				u.Status = "额度不限"
			} else {
				u.Metrics = append(u.Metrics, UsageMetric{Name: "剩余额度", Remaining: n, Unit: unit})
			}
		}
	}
	if len(u.Metrics) == 0 && u.Plan == "" && u.Status == "" {
		return u, errors.New("Sub2API 响应缺少余额或订阅字段，请检查部署版本")
	}
	return u, nil
}
func valueOrNil(n *float64) any {
	if n == nil {
		return nil
	}
	return *n
}
func jsonPath(d any, path string) any {
	if path == "" {
		return nil
	}
	var parts []string
	if strings.HasPrefix(path, "/") {
		parts = strings.Split(path[1:], "/")
		for i := range parts {
			parts[i] = strings.ReplaceAll(strings.ReplaceAll(parts[i], "~1", "/"), "~0", "~")
		}
	} else {
		path = strings.TrimPrefix(path, "$.")
		parts = strings.Split(path, ".")
	}
	for _, part := range parts {
		switch v := d.(type) {
		case map[string]any:
			d = v[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			d = v[i]
		default:
			return nil
		}
	}
	return d
}
func parseGeneric(d map[string]any, c UsageConfig) (UsageSnapshot, error) {
	get := func(k string) any { return jsonPath(d, c.Fields[k]) }
	if v, ok := get("is_valid").(bool); ok && !v {
		return UsageSnapshot{}, errors.New("服务商返回账户不可用")
	}
	u := UsageSnapshot{Plan: str(get("plan")), ExpiresAt: parseTime(get("expires_at"))}
	m := UsageMetric{Name: "账户额度", Remaining: number(get("remaining")), Used: number(get("used")), Total: number(get("total")), Unit: c.Unit}
	if unit := str(get("unit")); unit != "" {
		m.Unit = unit
	}
	scale := c.Scale
	if scale == 0 {
		scale = 1
	}
	for _, n := range []*float64{m.Remaining, m.Used, m.Total} {
		if n != nil {
			*n *= scale
		}
	}
	if m.Remaining == nil && m.Total != nil && m.Used != nil {
		n := *m.Total - *m.Used
		m.Remaining = &n
	}
	if m.Remaining != nil || m.Used != nil || m.Total != nil {
		u.Metrics = []UsageMetric{m}
	}
	if len(u.Metrics) == 0 && u.Plan == "" {
		return u, errors.New("未找到配置的查询字段；请检查字段路径")
	}
	return u, nil
}
