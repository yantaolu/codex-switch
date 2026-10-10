package switcher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func decodeResponse(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestOfficialUsageHelper(t *testing.T) {
	if os.Getenv("CXS_USAGE_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var r struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			os.Exit(2)
		}
		if strings.Contains(string(scanner.Bytes()), "refresh_token") {
			os.Exit(3)
		}
		var result any = map[string]any{}
		switch r.Method {
		case "initialize":
			if obj(r.Params["capabilities"])["experimentalApi"] != true {
				os.Exit(4)
			}
		case "initialized":
			continue
		case "account/login/start":
			if r.Params["type"] != "chatgptAuthTokens" || r.Params["accessToken"] == nil {
				os.Exit(5)
			}
		case "account/read":
			if r.Params["refreshToken"] != false {
				os.Exit(6)
			}
			result = map[string]any{"account": map[string]any{"planType": "pro"}}
		case "account/rateLimits/read":
			result = map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 20, "windowDurationMins": 300}}}
		default:
			os.Exit(7)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"id": r.ID, "result": result})
	}
	os.Exit(0)
}
func TestOfficialQueryIsolationAndRPC(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CXS_USAGE_HELPER", "1")
	t.Setenv("CXS_USAGE_TEST_BIN", bin)
	wrapper := filepath.Join(t.TempDir(), "codex")
	if err = os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$CXS_USAGE_TEST_BIN\" -test.run='^TestOfficialUsageHelper$'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, err := queryOfficial(ctx, wrapper, auth("alice", "never-send-this-refresh-token"))
	if err != nil || u.Plan != "pro" || len(u.Metrics) != 1 || *u.Metrics[0].Remaining != 80 {
		t.Fatalf("RPC failed: %+v %v", u, err)
	}
}
func TestSub2APIEndpointNormalization(t *testing.T) {
	for _, base := range []string{"https://example.com", "https://example.com/v1/", "https://example.com/v1?x=1"} {
		if got := Sub2APIUsageURL(base); got != "https://example.com/v1/usage" {
			t.Fatal(got)
		}
	}
}
func TestSub2APIAccountingModes(t *testing.T) {
	for _, tc := range []struct {
		body      string
		count     int
		remaining float64
		expires   bool
	}{
		{`{"mode":"unrestricted","balance":0,"planName":"钱包余额","unit":"USD"}`, 1, 0, false},
		{`{"planName":"Codex","subscription":{"daily_usage_usd":7,"daily_limit_usd":10,"weekly_usage_usd":30,"weekly_limit_usd":50,"monthly_limit_usd":null,"expires_at":"2026-11-01T00:00:00Z"}}`, 2, 3, true},
		{`{"mode":"quota_limited","quota":{"limit":100,"used":25,"remaining":75},"rate_limits":[{"window":"5h","limit":10,"used":15,"remaining":0,"reset_at":"2026-10-10T01:00:00Z"}]}`, 2, 75, false},
	} {
		u, err := parseSub2API(decodeResponse(t, tc.body))
		if err != nil || len(u.Metrics) != tc.count || u.Metrics[0].Remaining == nil || *u.Metrics[0].Remaining != tc.remaining || u.ExpiresAt.IsZero() == tc.expires {
			t.Fatalf("mode parse: %+v %v", u, err)
		}
	}
	u, err := parseSub2API(decodeResponse(t, `{"planName":"无限套餐","remaining":-1}`))
	if err != nil || len(u.Metrics) != 0 || u.Status != "额度不限" {
		t.Fatal("unlimited treated as negative balance")
	}
	if _, err = parseSub2API(map[string]any{"error": "bad"}); err == nil {
		t.Fatal("unknown response accepted")
	}
}
func TestGenericPathsScalingAndMissingValues(t *testing.T) {
	c := UsageConfig{Type: "generic", URL: "https://example.com/balance", Fields: map[string]string{"remaining": "/data/a~1b/0/balance", "used": "data.used", "plan": "data.plan"}, Scale: 1.0 / 500000, Unit: "USD"}
	u, err := parseGeneric(decodeResponse(t, `{"data":{"a/b":[{"balance":1000000}],"used":"500000","plan":"Pro"}}`), c)
	if err != nil || u.Plan != "Pro" || *u.Metrics[0].Remaining != 2 || *u.Metrics[0].Used != 1 {
		t.Fatalf("generic %+v %v", u, err)
	}
	if _, err = parseGeneric(map[string]any{}, c); err == nil {
		t.Fatal("missing values became zero")
	}
}
func TestProviderQueryCachePreservesGoodDataAndCredentials(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/usage" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("x-openai-actor-authorization") != "" {
			t.Error("wrong path or credentials")
		}
		if fail.Load() {
			w.WriteHeader(401)
			w.Write([]byte("test-key"))
			return
		}
		w.Write([]byte(`{"balance":9.25,"unit":"USD"}`))
	}))
	defer srv.Close()
	e, _ := fixture(t)
	if err := e.AddProvider("gateway", Provider{BaseURL: srv.URL + "/v1", Model: "m", APIKey: "test-key", Headers: map[string]string{"x-openai-actor-authorization": "image-only"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := snapshot(e.Home)
	vaultBefore, _ := os.ReadFile(filepath.Join(e.Store.Dir, "vault.enc"))
	if err := e.UpdateUsage("gateway", UsageConfig{Type: "sub2api"}); err != nil {
		t.Fatal(err)
	}
	configured, _ := os.ReadFile(filepath.Join(e.Store.Dir, "vault.enc"))
	if bytes.Equal(vaultBefore, configured) {
		t.Fatal("query settings not saved")
	}
	u, err := e.QueryUsage(context.Background(), "gateway", false)
	if err != nil || u.Error != "" || *u.Metrics[0].Remaining != 9.25 {
		t.Fatalf("fetch %+v %v", u, err)
	}
	e.QueryUsage(context.Background(), "gateway", false)
	if hits.Load() != 1 {
		t.Fatal("cache ignored")
	}
	fail.Store(true)
	u, err = e.QueryUsage(context.Background(), "gateway", true)
	if err != nil || u.Error == "" || strings.Contains(u.Error, "test-key") || *u.Metrics[0].Remaining != 9.25 || u.UpdatedAt.IsZero() {
		t.Fatal("failure lost data or leaked key")
	}
	after, _ := snapshot(e.Home)
	vaultAfter, _ := os.ReadFile(filepath.Join(e.Store.Dir, "vault.enc"))
	if !same(before, after) || !bytes.Equal(configured, vaultAfter) {
		t.Fatal("query changed active auth or vault")
	}
	cache, _ := os.ReadFile(filepath.Join(e.Store.Dir, "usage.enc"))
	if bytes.Contains(cache, []byte("9.25")) {
		t.Fatal("unencrypted cache")
	}
	st, _ := e.Store.Load()
	if st.Profiles["alice"].Usage != nil || st.Profiles["gateway"].Usage.Type != "sub2api" {
		t.Fatal("settings not per account")
	}
}
func TestBalanceRedirectDoesNotForwardKeys(t *testing.T) {
	var leaked atomic.Bool
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer dest.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dest.URL, 302) }))
	defer srv.Close()
	_, err := queryProvider(context.Background(), Profile{Provider: &Provider{BaseURL: srv.URL, APIKey: "private"}, Usage: &UsageConfig{Type: "sub2api", URL: srv.URL}})
	if err == nil || leaked.Load() {
		t.Fatal("balance key sent to redirect")
	}
}
func TestOfficialMultipleBucketsAndWindowTimes(t *testing.T) {
	d := decodeResponse(t, `{"rateLimits":{"primary":{"usedPercent":90}},"rateLimitsByLimitId":{"codex":{"planType":"pro","primary":{"usedPercent":25,"windowDurationMins":300,"resetsAt":1800000000},"secondary":{"usedPercent":100,"windowDurationMins":10080},"credits":{"balance":"12.5"}},"extra":{"primary":{"usedPercent":10,"windowDurationMins":15}}}}`)
	u, err := parseOfficialLimits(d)
	if err != nil || u.Plan != "pro" || len(u.Metrics) != 4 || *u.Metrics[0].Remaining != 75 || u.Metrics[0].ResetAt.IsZero() || *u.Metrics[1].Remaining != 0 {
		t.Fatalf("official %+v %v", u, err)
	}
}

func TestUsageUnitsWindowsAndTimestampFormats(t *testing.T) {
	second := int64(1791590400)
	for _, value := range []any{float64(second), json.Number("1791590400"), float64(second * 1000), "2026-10-10T00:00:00Z"} {
		got := parseTime(value)
		if got.Unix() != second || got.IsZero() {
			t.Fatalf("unexpected timestamp: %v %v", value, got)
		}
	}
	for minutes, want := range map[int]string{300: "5 小时", 10080: "7 天", 15: "15 分钟"} {
		if windowLabel(minutes) != want {
			t.Fatal("wrong quota window")
		}
	}
	u, err := parseOfficialLimits(map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": float64(25), "windowDurationMins": float64(300), "resetsAt": float64(second)}, "credits": map[string]any{"balance": "1.25"}}})
	if err != nil || len(u.Metrics) != 2 || u.Metrics[0].Unit != "%" || *u.Metrics[0].Remaining != 75 || u.Metrics[1].Unit != "credits" {
		t.Fatal("official units confused with cash")
	}
	sub, err := parseSub2API(map[string]any{"balance": json.Number("12.34"), "unit": "CNY"})
	if err != nil || len(sub.Metrics) != 1 || sub.Metrics[0].Unit != "CNY" {
		t.Fatal("provider currency lost")
	}
}
