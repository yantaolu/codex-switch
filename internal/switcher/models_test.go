package switcher

import (
	"errors"
	"testing"
)

func TestModelUpdatesAndRollback(t *testing.T) {
	e, r := fixture(t)
	p := Provider{BaseURL: "https://example.com/v1", Model: "old", APIKey: "dummy"}
	if err := e.AddProvider("relay", p); err != nil {
		t.Fatal(err)
	}
	before, _ := snapshot(e.Home)
	models := []Model{{ID: "one"}, {ID: "two", Name: "Two", ContextWindow: 32000}}
	if err := e.UpdateModels("relay", models, "two"); err != nil {
		t.Fatal(err)
	}
	after, _ := snapshot(e.Home)
	if !same(before, after) || r.stops != 0 {
		t.Fatal("inactive update touched live account")
	}
	if err := e.Use("relay"); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Store.Load()
	previous := st.Previous
	if err := e.UpdateModels("relay", models, "one"); err != nil {
		t.Fatal(err)
	}
	st, _ = e.Store.Load()
	if r.starts != 0 {
		t.Fatal("model update started the APP")
	}
	if st.Previous != previous || st.Profiles["relay"].Provider.Model != "one" {
		t.Fatal("active update lost previous account or default")
	}
	before, _ = snapshot(e.Home)
	r.verifyErr = errors.New("verification failed")
	if e.UpdateModels("relay", []Model{{ID: "new"}}, "new") == nil {
		t.Fatal("expected failure")
	}
	after, _ = snapshot(e.Home)
	st, _ = e.Store.Load()
	if !same(before, after) || st.Profiles["relay"].Provider.Model != "one" || len(st.Profiles["relay"].Provider.Models) != 2 {
		t.Fatal("failed update did not restore model list and live files")
	}
}

func TestModelValidationAndLegacy(t *testing.T) {
	p := Provider{Model: "legacy"}
	if len(Models(p)) != 1 || Models(p)[0].ID != "legacy" {
		t.Fatal("legacy model lost")
	}
	for _, models := range [][]Model{nil, {{ID: "x"}, {ID: "x"}}, {{ID: " x"}}, {{ID: "x", ContextWindow: 1}}} {
		if ValidateModels(models, "x") == nil {
			t.Fatal("invalid model list accepted")
		}
	}
	if ValidateModels([]Model{{ID: "x"}}, "missing") == nil {
		t.Fatal("missing default accepted")
	}
}

func TestRemoveNonCurrentPreviouslyUsedAccount(t *testing.T) {
	e, _ := fixture(t)
	if err := e.Use("bob"); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove("alice"); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Store.Load()
	if st.Current != "bob" || st.Previous != "" {
		t.Fatal("account removal left stale references")
	}
	if _, ok := st.Profiles["alice"]; ok {
		t.Fatal("account was not removed")
	}
	if e.Remove("bob") == nil {
		t.Fatal("current account removable")
	}
}
