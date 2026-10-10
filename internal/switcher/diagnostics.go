package switcher

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Diagnostics struct {
	Current          string
	Pending          bool
	AuthMatches      bool
	ConfiguredHelper string
	HelperMigration  bool
}

// Inspect only reads descriptors and local state. It does not query the network,
// refresh authentication, write a cache, or execute the configured helper.
func (e Engine) Inspect() (Diagnostics, error) {
	d := Diagnostics{Pending: e.Store.HasJournal()}
	st, err := e.state()
	if err != nil {
		return d, err
	}
	d.Current = st.Current
	if st.Current == "" {
		return d, nil
	}
	p := st.Profiles[st.Current]
	live, err := snapshot(e.Home)
	if err != nil {
		return d, err
	}
	config, err := ParseConfig(live.Config)
	if err != nil {
		return d, err
	}
	provider, _ := config["model_provider"].(string)
	if provider == "" {
		provider = "openai"
	}
	if p.Kind == "chatgpt" {
		a, ae := AuthIdentity(live.Auth)
		b, be := AuthIdentity(p.Auth)
		d.AuthMatches = ae == nil && be == nil && a == b && provider == "openai"
	}
	if p.Kind == "provider" && p.Provider != nil {
		var auth map[string]any
		d.AuthMatches = json.Unmarshal(live.Auth, &auth) == nil && auth["OPENAI_API_KEY"] == p.Provider.APIKey && provider == providerID
		entry := obj(obj(config["model_providers"])[providerID])
		helper := obj(entry["auth"])
		d.ConfiguredHelper, _ = helper["command"].(string)
		if d.ConfiguredHelper != "" {
			_, err := os.Stat(d.ConfiguredHelper)
			d.HelperMigration = err != nil || filepath.Clean(d.ConfiguredHelper) != filepath.Clean(e.Helper)
		}
	}
	return d, nil
}
