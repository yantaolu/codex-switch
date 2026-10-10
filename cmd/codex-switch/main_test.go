package main

import "testing"

func TestHelpAndVersionWithoutCredentials(t *testing.T) {
	for _, args := range [][]string{{}, {"help"}, {"version"}, {"--no-restart", "version"}, {"--help"}, {"-h"}, {"list", "--help"}, {"status", "--help"}, {"use", "--help"}, {"models", "--help"}, {"add", "chatgpt", "--help"}, {"add", "provider", "--help"}, {"remove", "--help"}, {"recover", "--help"}, {"ui", "--help"}} {
		if err := run(args); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemovedRestoreAndUnknownCommand(t *testing.T) {
	for _, args := range [][]string{{"restore"}, {"not-a-command"}, {"not-a-command", "--help"}} {
		if run(args) == nil {
			t.Fatal("unknown or removed command accepted")
		}
	}
}
func TestInvalidGlobalFlag(t *testing.T) {
	if run([]string{"--not-a-real-flag"}) == nil {
		t.Fatal("unknown flag accepted")
	}
}
