package switcher

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRepeatedSwitchesAreStable(t *testing.T) {
	b := []byte("model = 'old' # keep comment\n\n\nmodel_provider = 'openai'\n\n\ndeveloper_instructions = '''first\n\n\nlast'''\n\n[features]\ngoals = true\n\n\nimage_generation = false # keep feature comment\n\n\n[other]\ntext = '''a\n\n\nb'''\n")
	p := &Provider{BaseURL: "https://example.com/v1", Model: "m", APIKey: "secret", AuthMode: "bearer"}
	route := map[string]any{"features": map[string]any{"goals": true, "image_generation": true}}
	var previous []byte
	for i := 0; i < 20; i++ {
		var err error
		b, err = PatchConfig(b, route, p, "/helper", "/store", "work")
		if err != nil {
			t.Fatal(err)
		}
		b, err = PatchConfig(b, map[string]any{"model": "official"}, nil, "/helper", "/store", "official")
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && !bytes.Equal(previous, b) {
			t.Fatalf("configuration grows during cycle %d: %d -> %d", i, len(previous), len(b))
		}
		previous = append([]byte(nil), b...)
	}
	for _, v := range []string{"# keep comment", "# keep feature comment", "first\n\n\nlast", "a\n\n\nb"} {
		if !bytes.Contains(b, []byte(v)) {
			t.Fatal("unrelated content lost")
		}
	}
}

func TestImageFeatureImportAndIsolation(t *testing.T) {
	p, _, err := ImportTOML([]byte("model_provider='relay'\nmodel='m'\n[model_providers.relay]\nbase_url='https://example.com/v1'\nwire_api='responses'\nexperimental_bearer_token='secret'\nrequires_openai_auth=false\n[features]\nimage_generation=false\n"))
	if err != nil {
		t.Fatal(err)
	}
	features := p.Options["features"].(map[string]any)
	if features["image_generation"] != false {
		t.Fatal("feature lost")
	}
	b, err := PatchConfig(nil, p.Options, &p, "/helper", "/store", "relay")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := ParseConfig(b)
	if d["features"].(map[string]any)["image_generation"] != false {
		t.Fatal("feature not applied")
	}
	b, err = PatchConfig(b, nil, nil, "/helper", "/store", "official")
	if err != nil {
		t.Fatal(err)
	}
	d, _ = ParseConfig(b)
	if d["features"].(map[string]any)["image_generation"] != nil {
		t.Fatal("third-party feature leaked to official")
	}
}

func TestSwitchAuditSuccessFailureRollbackAndNoSecrets(t *testing.T) {
	e, r := fixture(t)
	if err := e.Use("bob"); err != nil {
		t.Fatal(err)
	}
	r.verifyErr = os.ErrPermission
	if err := e.Use("alice"); err == nil {
		t.Fatal("expected failure")
	}
	raw, err := os.ReadFile(filepath.Join(e.Store.Dir, "switch.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"access_token", "refresh_token", "bob-token", "Bearer", "config.toml"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("secret in log")
		}
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 4 {
		t.Fatal("missing audit events")
	}
	var last AuditEvent
	json.Unmarshal(lines[3], &last)
	if last.Status != "failed" || !last.RolledBack || last.Current != "bob" || last.Stage != "verify" {
		t.Fatalf("wrong result: %+v", last)
	}
	stat, _ := os.Stat(filepath.Join(e.Store.Dir, "switch.log"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal("unsafe log permissions")
	}
}

func TestNativeSessionForkPreservesSource(t *testing.T) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("native Codex not installed")
	}
	e, _ := fixture(t)
	e.Runtime = MacRuntime{Codex: bin, Home: e.Home}
	st, _ := e.Store.Load()
	st.Current = "relay"
	st.Profiles["relay"] = Profile{Kind: "provider", Provider: &Provider{BaseURL: "http://127.0.0.1:1/v1", Model: "gpt-6.1-sol", APIKey: "placeholder", AuthMode: "bearer"}}
	e.Store.Save(st)
	cfg := []byte("model_provider='codex_switch'\nmodel='gpt-6.1-sol'\ncli_auth_credentials_store='file'\n[model_providers.codex_switch]\nname='test'\nbase_url='http://127.0.0.1:1/v1'\nwire_api='responses'\nrequires_openai_auth=false\nexperimental_bearer_token='placeholder'\n")
	os.WriteFile(filepath.Join(e.Home, "config.toml"), cfg, 0600)
	os.WriteFile(filepath.Join(e.Home, "auth.json"), []byte(`{"OPENAI_API_KEY":"placeholder"}`), 0600)
	id := "01a0ccb3-1f5d-76d2-b36a-2e53db617c61"
	path := filepath.Join(e.Home, "sessions", "2026", "10", "09", "rollout-test-"+id+".jsonl")
	os.MkdirAll(filepath.Dir(path), 0700)
	meta := map[string]any{"timestamp": "2026-10-09T00:00:00Z", "type": "session_meta", "payload": map[string]any{"id": id, "timestamp": "2026-10-09T00:00:00Z", "cwd": e.Home, "originator": "codex_cli_rs", "cli_version": "0.162.0", "source": "cli", "model_provider": "openai"}}
	raw, _ := json.Marshal(meta)
	raw = append(raw, '\n')
	raw = append(raw, []byte("{\"timestamp\":\"2026-10-09T00:00:01Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"Test history\"}]}}\n")...)
	os.WriteFile(path, raw, 0600)
	newID, err := e.ContinueSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if newID == id {
		t.Fatal("source overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(raw, after) {
		t.Fatal("source changed")
	}
	after, _ = os.ReadFile(filepath.Join(e.Home, "config.toml"))
	if !bytes.Equal(after, cfg) {
		t.Fatal("config changed")
	}
	sessions, err := e.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range sessions {
		if s.ID == newID && s.Provider == providerID {
			found = true
		}
	}
	if !found {
		t.Fatal("fork not persisted under target provider")
	}
	var containsHistory bool
	filepath.WalkDir(filepath.Join(e.Home, "sessions"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(p, newID) {
			v, _ := os.ReadFile(p)
			containsHistory = bytes.Contains(v, []byte("Test history"))
		}
		return nil
	})
	if !containsHistory {
		t.Fatal("history lost")
	}
	// Switching back can copy the third-party thread into an official thread too.
	st.Current = "alice"
	e.Store.Save(st)
	os.WriteFile(filepath.Join(e.Home, "auth.json"), st.Profiles["alice"].Auth, 0600)
	os.WriteFile(filepath.Join(e.Home, "config.toml"), []byte("model_provider='openai'\ncli_auth_credentials_store='file'\n"), 0600)
	officialID, err := e.ContinueSession(context.Background(), newID)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err = e.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, s := range sessions {
		if s.ID == officialID && s.Provider == "openai" {
			found = true
		}
	}
	if !found {
		t.Fatal("reverse fork provider incorrect")
	}
}
