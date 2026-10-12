package switcher

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

func legacyRelay(t *testing.T) Provider {
	t.Helper()
	p, _, err := ImportTOML([]byte(sampleRelay))
	if err != nil {
		t.Fatal(err)
	}
	p.Options["disable_response_storage"] = true
	p.Options["network_access"] = "enabled"
	p.Options["windows_wsl_setup_acknowledged"] = true
	return p
}

func assertCleanAccountOptions(t *testing.T, options map[string]any) {
	t.Helper()
	for _, key := range []string{"disable_response_storage", "network_access", "windows_wsl_setup_acknowledged"} {
		if _, exists := options[key]; exists {
			t.Errorf("filtered field %s remains", key)
		}
	}
}

func TestRelayImportFiltersAndFormatsWithoutLosingAuthorization(t *testing.T) {
	p, warnings, err := ImportTOML([]byte(sampleRelay))
	if err != nil {
		t.Fatal(err)
	}
	assertCleanAccountOptions(t, p.Options)
	if len(warnings) != 3 {
		t.Fatal("expected three cleanup diagnostics")
	}
	for _, warning := range warnings {
		if !strings.Contains(warning, "已过滤") || strings.Contains(warning, p.APIKey) {
			t.Fatal("cleanup diagnostic is missing or exposes credentials")
		}
	}
	for i := 0; i < 5; i++ {
		formatted, err := ProviderTOML(p)
		if err != nil {
			t.Fatal(err)
		}
		parsed, nextWarnings, err := ImportTOML(formatted)
		if err != nil || len(nextWarnings) != 0 || !reflect.DeepEqual(p, parsed) {
			t.Fatalf("formatted import changed valid options: %v", err)
		}
		next, err := ProviderTOML(parsed)
		if err != nil || !bytes.Equal(formatted, next) || bytes.Contains(next, []byte("\n\n\n")) {
			t.Fatal("formatting does not converge")
		}
		p = parsed
	}
	if p.APIKey != "sk-isolated-dummy" || p.CatalogMode != "remote" || p.CatalogURL != "https://api.example/v1/models" || p.Headers["x-openai-actor-authorization"] != "local-image-extension" {
		t.Fatal("bearer or image/model discovery authorization changed")
	}
}

func TestIgnoredAccountFieldsDoNotRequireValidTypes(t *testing.T) {
	raw := strings.ReplaceAll(sampleRelay, "disable_response_storage = true", "disable_response_storage = { invalid = 'ignored' }")
	raw = strings.ReplaceAll(raw, "network_access = \"enabled\"", "network_access = 123")
	raw = strings.ReplaceAll(raw, "windows_wsl_setup_acknowledged = true", "windows_wsl_setup_acknowledged = 'ignored'")
	p, warnings, err := ImportTOML([]byte(raw))
	if err != nil || len(warnings) != 3 {
		t.Fatalf("ignored fields must not block import: %v", err)
	}
	assertCleanAccountOptions(t, p.Options)
}

func TestLegacyProviderExportCleansOnlyAccountOptions(t *testing.T) {
	p := legacyRelay(t)
	before := p.Options["network_access"]
	formatted, err := ProviderTOML(p)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseConfig(formatted)
	if err != nil {
		t.Fatal(err)
	}
	assertCleanAccountOptions(t, d)
	if p.Options["network_access"] != before {
		t.Fatal("export mutated the saved account draft")
	}
	parsed, warnings, err := ImportTOML(formatted)
	if err != nil || len(warnings) != 0 || parsed.APIKey != p.APIKey || parsed.CatalogURL != p.CatalogURL || !reflect.DeepEqual(parsed.Headers, p.Headers) {
		t.Fatalf("legacy export lost provider settings: %v", err)
	}
}

func TestConfigCleanupPreservesNestedNetworkPolicyAndMultilineText(t *testing.T) {
	for _, obsolete := range []string{
		"disable_response_storage = true\nnetwork_access = 'enabled'\nwindows_wsl_setup_acknowledged = true\n",
		"disable_response_storage = { ignored = true }\nnetwork_access.policy = 'ignored'\nwindows_wsl_setup_acknowledged = 1\n",
		"[disable_response_storage]\nignored = true\n[network_access.policy]\nignored = true\n[windows_wsl_setup_acknowledged]\nignored = true\n",
	} {
		b := []byte("developer_instructions = '''network_access = 'text'\n\n\nkeep this'''\n" + obsolete +
			"\n[sandbox_workspace_write]\n# preserve network policy\nnetwork_access = true\nwritable_roots = ['/tmp/work']\n" +
			"\n[model_providers.other]\nbase_url = 'https://other.example/v1'\n")
		route := map[string]any{"network_access": "enabled", "disable_response_storage": true, "windows_wsl_setup_acknowledged": true}
		out, err := PatchConfig(b, route, nil, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		d, err := ParseConfig(out)
		if err != nil {
			t.Fatal(err)
		}
		assertCleanAccountOptions(t, d)
		if d["sandbox_workspace_write"].(map[string]any)["network_access"] != true || !bytes.Contains(out, []byte("# preserve network policy\nnetwork_access = true")) || !bytes.Contains(out, []byte("\n\n\nkeep this")) {
			t.Fatal("cleanup changed shared policy, comments or string values")
		}
		next, err := PatchConfig(out, route, nil, "", "", "")
		if err != nil || !bytes.Equal(out, next) {
			t.Fatal("repeated cleanup changed formatted config")
		}
		captured, err := Route(b)
		if err != nil {
			t.Fatal(err)
		}
		assertCleanAccountOptions(t, captured)
	}
}

func TestLegacyVaultReadIsReadOnlyAndNextSaveCleansFields(t *testing.T) {
	e, _ := fixture(t)
	st, _ := e.Store.Load()
	p := legacyRelay(t)
	st.Profiles["relay"] = Profile{Kind: "provider", Provider: &p}
	st.Profiles["bob"] = Profile{Kind: "chatgpt", Auth: auth("bob", "bob-token"), Route: map[string]any{"model": "gpt-official", "network_access": "enabled"}}
	st.Baseline = &Snapshot{Config: []byte("network_access = 'enabled'\n"), ConfigExists: true}
	if err := e.Store.write("vault.enc", st); err != nil { // Simulate an existing pre-cleanup vault.
		t.Fatal(err)
	}
	before, _ := readSafe(e.Store.Dir + "/vault.enc")
	clean, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	after, _ := readSafe(e.Store.Dir + "/vault.enc")
	if !bytes.Equal(before, after) {
		t.Fatal("reading an old vault rewrote it")
	}
	assertCleanAccountOptions(t, clean.Profiles["relay"].Provider.Options)
	assertCleanAccountOptions(t, clean.Profiles["bob"].Route)
	if !bytes.Equal(clean.Profiles["bob"].Auth, st.Profiles["bob"].Auth) || !reflect.DeepEqual(clean.Baseline, st.Baseline) || clean.Profiles["relay"].Provider.APIKey != p.APIKey {
		t.Fatal("cleanup altered credentials or rollback snapshots")
	}
	if err := e.Store.Save(st); err != nil {
		t.Fatal(err)
	}
	var saved State
	if err := e.Store.read("vault.enc", &saved); err != nil {
		t.Fatal(err)
	}
	assertCleanAccountOptions(t, saved.Profiles["relay"].Provider.Options)
	assertCleanAccountOptions(t, saved.Profiles["bob"].Route)
	if p.Options["network_access"] != "enabled" || st.Profiles["bob"].Route["network_access"] != "enabled" {
		t.Fatal("save changed its caller's original state")
	}
}

func TestLegacyAccountSwitchCleansFieldsAndFailureRestoresSnapshot(t *testing.T) {
	e, runtime := fixture(t)
	st, _ := e.Store.Load()
	p := legacyRelay(t)
	st.Profiles["relay"] = Profile{Kind: "provider", Provider: &p}
	if err := e.Store.write("vault.enc", st); err != nil {
		t.Fatal(err)
	}
	before, _ := snapshot(e.Home)
	before.Config = append([]byte("disable_response_storage = true\nnetwork_access = 'enabled'\nwindows_wsl_setup_acknowledged = true\n"), before.Config...)
	if err := applySnapshot(e.Home, before); err != nil {
		t.Fatal(err)
	}
	runtime.verifyErr = os.ErrPermission
	if err := e.Use("relay"); err == nil || ResultFor(err).Status != OutcomeRolledBack {
		t.Fatal("switch should roll back after verification failure")
	}
	restored, _ := snapshot(e.Home)
	if !same(before, restored) {
		t.Fatal("rollback did not restore the original configuration byte-for-byte")
	}
	runtime.verifyErr = nil
	if err := e.Use("relay"); err != nil {
		t.Fatal(err)
	}
	live, _ := snapshot(e.Home)
	d, err := ParseConfig(live.Config)
	if err != nil {
		t.Fatal(err)
	}
	assertCleanAccountOptions(t, d)
	ps := d["model_providers"].(map[string]any)[providerID].(map[string]any)
	if ps["experimental_bearer_token"] != p.APIKey || ps["model_catalog_url"] != p.CatalogURL || ps["http_headers"].(map[string]any)["x-openai-actor-authorization"] != "local-image-extension" {
		t.Fatal("successful switch lost bearer, models or image authorization")
	}
	if err := e.Use("relay"); err != nil {
		t.Fatal(err)
	}
	second, _ := snapshot(e.Home)
	if !same(live, second) {
		t.Fatalf("repeat switch changed configuration; auth changed=%v\nfirst:\n%s\nsecond:\n%s", !bytes.Equal(live.Auth, second.Auth), strings.ReplaceAll(string(live.Config), p.APIKey, "[test-key]"), strings.ReplaceAll(string(second.Config), p.APIKey, "[test-key]"))
	}
}

func TestNormalizedSaveOutcomeMatchesCommittedState(t *testing.T) {
	e, _ := fixture(t)
	st, _ := e.Store.Load()
	p := legacyRelay(t)
	st.Profiles["relay"] = Profile{Kind: "provider", Provider: &p}
	e.Store.fault = func(stage string) error {
		if stage == "atomic:vault.enc:before_directory_sync" {
			return os.ErrPermission
		}
		return nil
	}
	if err := e.Store.Save(st); err == nil || !IsCommitted(err) || IsUnconfirmed(err) {
		t.Fatalf("normalized state was not recognized after a committed write: %v", err)
	}
	e.Store.fault = nil
	var saved State
	if err := e.Store.read("vault.enc", &saved); err != nil {
		t.Fatal(err)
	}
	assertCleanAccountOptions(t, saved.Profiles["relay"].Provider.Options)
}
