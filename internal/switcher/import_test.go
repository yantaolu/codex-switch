package switcher

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

const sampleRelay = `model_provider = "OpenAI"
model = "gpt-5.5"
review_model = "gpt-5.5"
disable_response_storage = true
network_access = "enabled"
windows_wsl_setup_acknowledged = true

[model_providers.OpenAI]
name = "OpenAI"
base_url = "https://api.example/v1"
model_catalog_url = "https://api.example/v1/models"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-isolated-dummy"
http_headers = { "x-openai-actor-authorization" = "local-image-extension" }

[features]
api_key_model_discovery = true
goals = true
`

func TestImportRelayRoundTripAndRollback(t *testing.T) {
	p, warnings, err := ImportTOML([]byte(sampleRelay))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 3 || p.CatalogMode != "remote" || p.Headers["x-openai-actor-authorization"] != "local-image-extension" {
		t.Fatal("import lost compatibility fields")
	}
	e, r := fixture(t)
	// Keep unrelated feature entries and their comments byte-for-byte.
	before, _ := snapshot(e.Home)
	before.Config = append(before.Config, []byte("\n[features]\n# retained feature\nshell_tool = true\ngoals = false\n")...)
	if err = applySnapshot(e.Home, before); err != nil {
		t.Fatal(err)
	}
	if err = e.AddProvider("relay", p); err != nil {
		t.Fatal(err)
	}
	if err = e.Use("relay"); err != nil {
		t.Fatal(err)
	}
	after, _ := snapshot(e.Home)
	d, err := ParseConfig(after.Config)
	if err != nil {
		t.Fatal(err)
	}
	if d["model"] != "gpt-5.5" || d["review_model"] != "gpt-5.5" || d["disable_response_storage"] != true || d["model_catalog_json"] != nil {
		t.Fatal("route fields wrong")
	}
	provider := d["model_providers"].(map[string]any)[providerID].(map[string]any)
	if provider["experimental_bearer_token"] != p.APIKey || provider["auth"] != nil || provider["model_catalog_url"] != p.CatalogURL || provider["requires_openai_auth"] != false {
		t.Fatal("bearer and discovery configuration lost")
	}
	features := d["features"].(map[string]any)
	if features["goals"] != true || features["api_key_model_discovery"] != true || features["shell_tool"] != true || !bytes.Contains(after.Config, []byte("# retained feature\nshell_tool = true")) {
		t.Fatal("feature ownership incorrect")
	}
	var credentials map[string]any
	json.Unmarshal(after.Auth, &credentials)
	if credentials["OPENAI_API_KEY"] != p.APIKey {
		t.Fatal("API Key Mode not preserved")
	}
	encrypted, err := readSafe(e.Store.Dir + "/vault.enc")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(p.APIKey)) {
		t.Fatal("vault exposes key")
	}
	r.verifyErr = errors.New("failed")
	if e.UpdateModels("relay", []Model{{ID: "other"}}, "other") == nil {
		t.Fatal("expected rollback")
	}
	live, _ := snapshot(e.Home)
	st, _ := e.Store.Load()
	if !same(after, live) || st.Profiles["relay"].Provider.CatalogMode != "remote" {
		t.Fatal("rollback lost remote directory")
	}
	r.verifyErr = nil
	if err = e.Use("alice"); err != nil {
		t.Fatal(err)
	}
	live, _ = snapshot(e.Home)
	d, _ = ParseConfig(live.Config)
	if d["review_model"] != nil || d["disable_response_storage"] != nil {
		t.Fatal("provider options leaked into official account")
	}
	features = d["features"].(map[string]any)
	if features["goals"] != false || features["api_key_model_discovery"] != nil || features["shell_tool"] != true {
		t.Fatal("official features not restored")
	}
	if bytes.Contains(live.Config, []byte(p.APIKey)) {
		t.Fatal("relay bearer left in live config")
	}
}

func TestNativeImportedRemoteCatalog(t *testing.T) {
	bin := os.Getenv("CODEX_SWITCH_NATIVE_CLI")
	if bin == "" {
		t.Skip("set CODEX_SWITCH_NATIVE_CLI")
	}
	p, _, err := ImportTOML([]byte(sampleRelay))
	if err != nil {
		t.Fatal(err)
	}
	p.Model = "remote-sample"
	p.Models = []Model{{ID: "remote-sample"}, {ID: "remote-second"}}
	catalog, err := Catalog(p)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+p.APIKey || r.Header.Get("x-openai-actor-authorization") != "local-image-extension" {
			http.Error(w, "missing compatibility authorization", http.StatusUnauthorized)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write(catalog)
	}))
	defer server.Close()
	p.BaseURL = server.URL + "/v1"
	p.CatalogURL = p.BaseURL + "/models"
	e, _ := fixture(t)
	before, _ := snapshot(e.Home)
	after, err := e.plan(before, Profile{Kind: "provider", Provider: &p}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if err = applySnapshot(e.Home, after); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "login", "status")
	cmd.Env = scopedEnv(e.Home)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native import config rejected: %s", output)
	}
	assertNativeModels(t, bin, e.Home, "remote-sample", "remote-second")
	if calls.Load() == 0 {
		t.Fatal("native Codex did not discover authenticated remote catalog")
	}
}

func TestImportRejectsUnsafeAndReportsUnknown(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(sampleRelay, "wire_api = \"responses\"", "wire_api = \"chat\"", 1),
		strings.Replace(sampleRelay, "requires_openai_auth = false", "requires_openai_auth = true", 1),
		strings.Replace(sampleRelay, "https://api.example/v1/models", "file:///tmp/models.json", 1),
		sampleRelay + "badflag = true\n",
	} {
		p, w, err := ImportTOML([]byte(raw))
		if strings.Contains(raw, "badflag") {
			if err != nil || len(w) != 4 || p.Model == "" {
				t.Fatal("unknown field not reported")
			}
		} else if err == nil {
			t.Fatal("unsafe import accepted")
		}
	}
	raw := strings.Replace(sampleRelay, "experimental_bearer_token = \"sk-isolated-dummy\"", "env_key = \"SECRET\"", 1)
	if _, _, err := ImportTOML([]byte(raw)); err == nil {
		t.Fatal("environment credential import accepted")
	}
	if _, _, err := ImportTOML([]byte("experimental_bearer_token = 'never-echo-me'\n[broken")); err == nil || strings.Contains(err.Error(), "never-echo-me") {
		t.Fatal("parser diagnostic leaks source")
	}
}

func TestLocalAndRemoteCatalogMode(t *testing.T) {
	p, _, _ := ImportTOML([]byte(sampleRelay))
	e, r := fixture(t)
	if err := e.AddProvider("relay", p); err != nil {
		t.Fatal(err)
	}
	if err := e.UpdateModels("relay", []Model{{ID: "local"}}, "local"); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Store.Load()
	if st.Profiles["relay"].Provider.CatalogMode != "local" {
		t.Fatal("manual model list did not select local mode")
	}
	if err := e.UseRemoteCatalog("relay"); err != nil {
		t.Fatal(err)
	}
	st, _ = e.Store.Load()
	if st.Profiles["relay"].Provider.CatalogMode != "remote" || r.stops != 0 {
		t.Fatal("inactive directory change restarted app")
	}
	if err := e.Use("relay"); err != nil {
		t.Fatal(err)
	}
	if err := e.UpdateModels("relay", []Model{{ID: "local"}}, "local"); err != nil {
		t.Fatal(err)
	}
	live, _ := snapshot(e.Home)
	d, _ := ParseConfig(live.Config)
	provider := d["model_providers"].(map[string]any)[providerID].(map[string]any)
	if d["model_catalog_json"] == nil || provider["model_catalog_url"] != nil {
		t.Fatal("manual and remote catalogs mixed")
	}
	if err := e.UseRemoteCatalog("relay"); err != nil {
		t.Fatal(err)
	}
	live, _ = snapshot(e.Home)
	d, _ = ParseConfig(live.Config)
	provider = d["model_providers"].(map[string]any)[providerID].(map[string]any)
	if d["model_catalog_json"] != nil || provider["model_catalog_url"] != p.CatalogURL {
		t.Fatal("remote catalog not restored")
	}
}

func TestFlexibleProviderEndpoints(t *testing.T) {
	raw := strings.Replace(sampleRelay, "https://api.example/v1\"", "http://api.example/v1?region=local\"", 1)
	raw = strings.Replace(raw, "https://api.example/v1/models", "https://catalog.example/models?version=1", 1)
	p, _, err := ImportTOML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if p.CatalogURL != "https://catalog.example/models?version=1" {
		t.Fatal("configured catalog endpoint changed")
	}
}
