package switcher

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type usageRPC struct {
	scan *bufio.Scanner
	out  io.Writer
	id   int
}

func (r *usageRPC) call(method string, params any) (json.RawMessage, error) {
	r.id++
	if err := json.NewEncoder(r.out).Encode(map[string]any{"id": r.id, "method": method, "params": params}); err != nil {
		return nil, errors.New("无法发送官方查询")
	}
	for r.scan.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(r.scan.Bytes(), &msg) != nil {
			return nil, errors.New("Codex 原生响应无法解析")
		}
		// External auth never rotates stored refresh tokens. Refuse refresh requests.
		if msg.Method != "" && len(msg.ID) > 0 {
			json.NewEncoder(r.out).Encode(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32001, "message": "Refresh unavailable in read-only query"}})
			continue
		}
		var id int
		if json.Unmarshal(msg.ID, &id) != nil || id != r.id {
			continue
		}
		if len(msg.Error) > 0 && string(msg.Error) != "null" {
			// Never forward server error text, which may contain credential-bearing URLs.
			if method == "account/rateLimits/read" {
				return nil, &RPCRejectedError{Message: "官方额度查询失败；登录可能过期，或服务暂不可用"}
			}
			return nil, &RPCRejectedError{Message: "Codex CLI 不支持原生协议，或请求被拒绝"}
		}
		if len(msg.Result) == 0 || string(msg.Result) == "null" {
			return nil, errors.New("Codex 原生响应缺少结果")
		}
		return msg.Result, nil
	}
	return nil, errors.New("官方查询进程中断或超时")
}
func queryOfficial(ctx context.Context, bin string, auth []byte) (UsageSnapshot, error) {
	var a struct {
		Tokens struct {
			Access  string `json:"access_token"`
			ID      string `json:"id_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(auth, &a) != nil || a.Tokens.Access == "" || a.Tokens.Account == "" {
		return UsageSnapshot{}, errors.New("官方登录凭据不完整")
	}
	if exp := number(jwtClaims(a.Tokens.Access)["exp"]); exp != nil && int64(*exp) <= time.Now().Unix() {
		return UsageSnapshot{}, errors.New("官方登录凭据已过期；请切换到该账户完成重新认证后再查询")
	}
	plan := ""
	for _, token := range []string{a.Tokens.ID, a.Tokens.Access} {
		claims := jwtClaims(token)
		if v := str(obj(claims["https://api.openai.com/auth"])["chatgpt_plan_type"]); v != "" {
			plan = v
			break
		}
	}
	home, err := os.MkdirTemp("", "codex-switch-query-*")
	if err != nil {
		return UsageSnapshot{}, errors.New("无法创建查询目录")
	}
	defer os.RemoveAll(home)
	// The temp server has neither refresh tokens nor the live user's config/plugins.
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0600); err != nil {
		return UsageSnapshot{}, err
	}
	client, err := startNative(ctx, bin, home, true)
	if err != nil {
		return UsageSnapshot{}, err
	}
	defer client.Close()
	rpc := &client.rpc
	if _, err = rpc.call("account/login/start", map[string]any{"type": "chatgptAuthTokens", "accessToken": a.Tokens.Access, "chatgptAccountId": a.Tokens.Account, "chatgptPlanType": plan}); err != nil {
		return UsageSnapshot{}, err
	}
	account, err := rpc.call("account/read", map[string]any{"refreshToken": false})
	if err != nil {
		return UsageSnapshot{}, err
	}
	var info struct {
		Account struct {
			Plan string `json:"planType"`
		} `json:"account"`
	}
	json.Unmarshal(account, &info)
	if info.Account.Plan != "" {
		plan = info.Account.Plan
	}
	limits, err := rpc.call("account/rateLimits/read", nil)
	if err != nil {
		return UsageSnapshot{}, err
	}
	var d map[string]any
	if json.Unmarshal(limits, &d) != nil {
		return UsageSnapshot{}, errors.New("官方额度响应无效")
	}
	u, err := parseOfficialLimits(d)
	if u.Plan == "" {
		u.Plan = plan
	}
	return u, err
}
func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var d map[string]any
	json.Unmarshal(b, &d)
	return d
}
func parseOfficialLimits(d map[string]any) (UsageSnapshot, error) {
	u := UsageSnapshot{}
	buckets := obj(d["rateLimitsByLimitId"])
	if len(buckets) == 0 {
		if single := obj(d["rateLimits"]); single != nil {
			buckets = map[string]any{"codex": single}
		}
	}
	keys := []string{}
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bucket := obj(buckets[key])
		if p := str(bucket["planType"]); p != "" {
			u.Plan = p
		}
		label := str(bucket["limitName"])
		if label == "" {
			label = key
		}
		for _, window := range []string{"primary", "secondary"} {
			w := obj(bucket[window])
			used := number(w["usedPercent"])
			if used == nil {
				continue
			}
			remaining := math.Max(0, math.Min(100, 100-*used))
			total := float64(100)
			period := window
			if n := number(w["windowDurationMins"]); n != nil {
				period = windowLabel(int(*n))
			}
			u.Metrics = append(u.Metrics, UsageMetric{Name: label + " · " + period, Remaining: &remaining, Used: used, Total: &total, Unit: "%", ResetAt: parseTime(w["resetsAt"])})
		}
		credits := obj(bucket["credits"])
		if b := number(credits["balance"]); b != nil {
			u.Metrics = append(u.Metrics, UsageMetric{Name: label + " credits", Remaining: b, Unit: "credits"})
		}
		if unlimited, ok := credits["unlimited"].(bool); ok && unlimited {
			u.Status = "credits 不限"
		}
	}
	if len(u.Metrics) == 0 && u.Status == "" {
		return u, errors.New("官方接口暂未返回额度数据")
	}
	return u, nil
}
func windowLabel(mins int) string {
	if mins > 0 && mins%1440 == 0 {
		return fmt.Sprintf("%d 天", mins/1440)
	}
	if mins > 0 && mins%60 == 0 {
		return fmt.Sprintf("%d 小时", mins/60)
	}
	return fmt.Sprintf("%d 分钟", mins)
}
