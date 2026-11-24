package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// accountsAntigravityBuiltinCmd is `magpie accounts antigravity-builtin
// [on|off]`: whether magpie's own Antigravity subscription serves its
// accounts on this computer, Google's Code Assist asked directly as a
// native provider (settings.AntigravityBuiltin). With no argument it says
// which is in effect.
func accountsAntigravityBuiltinCmd(args []string) error {
	if len(args) > 1 || len(args) == 1 && args[0] != "on" && args[0] != "off" {
		return fmt.Errorf("usage: magpie accounts antigravity-builtin [on|off]")
	}
	if len(args) == 1 {
		if args[0] == "on" {
			fmt.Println(bold.Render("!"), provider.AntigravityRisk)
		}
		s := settings.Load()
		s.AntigravityBuiltin = args[0] == "on"
		if err := settings.Save(s); err != nil {
			return err
		}
		provider.ForgetAccounts()
	}
	env := strings.TrimSpace(os.Getenv("MAGPIE_ANTIGRAVITY_BUILTIN")) != ""
	switch {
	case env && settings.AntigravityBuiltin() != settings.Shared().AntigravityBuiltin:
		fmt.Printf("built-in Antigravity is %s for this run: MAGPIE_ANTIGRAVITY_BUILTIN is set (saved: %s)\n", onOff(settings.AntigravityBuiltin()), onOff(settings.Shared().AntigravityBuiltin))
	case settings.AntigravityBuiltin():
		fmt.Println("built-in Antigravity is on: its accounts are served by magpie itself, Code Assist asked directly, and not moved onto a plugin")
	default:
		fmt.Println("built-in Antigravity is off: retired, as magpie ships it")
	}
	return nil
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
