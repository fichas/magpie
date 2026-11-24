package cli

import (
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

// `magpie accounts antigravity-builtin on` saves the opt-in, says the risk,
// and leaves every other setting as it was; `off` takes it back, the
// variable wins for the run, and any other word is refused.
func TestAccountsAntigravityBuiltinCmd(t *testing.T) {
	t.Setenv("MAGPIE_ANTIGRAVITY_BUILTIN", "")
	if err := settings.Save(settings.Settings{Currency: "cny"}); err != nil {
		t.Fatal(err)
	}
	say := func(args ...string) string {
		t.Helper()
		out, err := printed(t, func() error { return run(append([]string{"accounts", "antigravity-builtin"}, args...)) })
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	if out := say(); !strings.Contains(out, "is off") {
		t.Errorf("default: %q", out)
	}
	if out := say("on"); !strings.Contains(out, "is on") || !strings.Contains(out, "!") {
		t.Errorf("on: %q", out)
	}
	if s := settings.Load(); !s.AntigravityBuiltin || s.Currency != "cny" {
		t.Errorf("saved %+v", s)
	}
	t.Setenv("MAGPIE_ANTIGRAVITY_BUILTIN", "0")
	if out := say(); !strings.Contains(out, "MAGPIE_ANTIGRAVITY_BUILTIN") || !strings.Contains(out, "saved: on") {
		t.Errorf("under the variable: %q", out)
	}
	t.Setenv("MAGPIE_ANTIGRAVITY_BUILTIN", "")
	say("off")
	if settings.Load().AntigravityBuiltin {
		t.Error("off left it on")
	}
	if err := run([]string{"accounts", "antigravity-builtin", "maybe"}); err == nil {
		t.Error("a word other than on or off taken")
	}
}

// prompt=agy turns the AGY profile on and as-written off, as-written the
// other way round, default both off; any other value leaves them alone.
func TestProviderSetPromptAgy(t *testing.T) {
	groupsHome(t)
	set := func(v string) error {
		_, err := printed(t, func() error { return providerCmd([]string{"provider", "set", "a", "prompt=" + v}) })
		return err
	}
	if err := set("as-written"); err != nil {
		t.Fatal(err)
	}
	if err := set("agy"); err != nil {
		t.Fatal(err)
	}
	if p := mustFind(t, "a"); !p.AgyCompat || p.PromptAsWritten {
		t.Fatalf("prompt=agy: agy %v, as written %v", p.AgyCompat, p.PromptAsWritten)
	}
	if err := set("maybe"); err == nil || !strings.Contains(err.Error(), "prompt=as-written|agy|default") {
		t.Fatalf("prompt=maybe: %v", err)
	}
	if !mustFind(t, "a").AgyCompat {
		t.Fatal("a refused value turned it off")
	}
	if err := set("as-written"); err != nil {
		t.Fatal(err)
	}
	if p := mustFind(t, "a"); p.AgyCompat || !p.PromptAsWritten {
		t.Fatalf("prompt=as-written: agy %v, as written %v", p.AgyCompat, p.PromptAsWritten)
	}
	if err := set("default"); err != nil {
		t.Fatal(err)
	}
	if p := mustFind(t, "a"); p.AgyCompat || p.PromptAsWritten {
		t.Fatalf("prompt=default: agy %v, as written %v", p.AgyCompat, p.PromptAsWritten)
	}
}
