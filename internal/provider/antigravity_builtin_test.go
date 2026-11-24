package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/settings"
)

// With settings.AntigravityBuiltin on, the retired built-in serves its
// saved accounts again, as Gemini CLI's on the same code are, and magpie
// moves none of them onto the withdrawn plugin by itself: no install is
// tried, and the account stays in logins.json. Turned off again it is
// retired as before.
func TestAntigravityBuiltinServesAndStays(t *testing.T) {
	f := &fakeGoogle{quota: `{"buckets":[]}`, models: `{"models":{}}`}
	googleSandbox(t, f)
	t.Setenv("MAGPIE_ANTIGRAVITY_BUILTIN", "")
	installs := 0
	old := installPlugin
	installPlugin = func(context.Context, string, string) error {
		installs++
		return errors.New("no installs in this test")
	}
	t.Cleanup(func() { installPlugin = old })

	exp := time.Now().Add(time.Hour).UnixMilli()
	if err := addGoogleLogin("antigravity", "ag@example.com", "", googleAuth{AccessToken: "at-ag", RefreshToken: "rt-ag", Project: "proj-ag", Expiry: exp}); err != nil {
		t.Fatal(err)
	}
	s := settings.Load()
	s.AntigravityBuiltin = true
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	ForgetAccounts()

	if PluginServes("antigravity") || BuiltinRetired("antigravity") {
		t.Fatal("the built-in is still retired with antigravityBuiltin on")
	}
	if ls := googleLogins("antigravity"); len(ls) != 1 || ls[0].User != "ag@example.com" {
		t.Fatalf("googleLogins(antigravity) = %+v, want the saved account", ls)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	moves := MoveRetiring(ctx)
	for id, err := range HandOver(ctx, true) {
		moves[id] = err
	}
	if _, ok := moves["antigravity"]; ok || installs != 0 || Moved("antigravity") {
		t.Fatalf("antigravity was moved: %v, %d installs, moved %v", moves, installs, Moved("antigravity"))
	}
	if _, ok := MigrationOf("antigravity"); ok {
		t.Fatal("a migration was recorded for antigravity")
	}
	if ls := googleSavedLogins("antigravity"); len(ls) != 1 {
		t.Fatalf("the saved account went: %+v", ls)
	}

	// the variable wins for this run, either way
	t.Setenv("MAGPIE_ANTIGRAVITY_BUILTIN", "0")
	if !BuiltinRetired("antigravity") {
		t.Fatal("MAGPIE_ANTIGRAVITY_BUILTIN=0 didn't retire it for this run")
	}
	t.Setenv("MAGPIE_ANTIGRAVITY_BUILTIN", "")
	s.AntigravityBuiltin = false
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	ForgetAccounts()
	if !BuiltinRetired("antigravity") || len(googleLogins("antigravity")) != 0 {
		t.Fatal("turned off, the built-in still serves")
	}
}
