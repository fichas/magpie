package settings

import (
	"os"
	"strings"

	"github.com/yetone/magpie/internal/filememo"
)

// AccountDiscoveryOff is whether magpie leaves agents' sign-ins unread
// (#1535): NoAccountDiscovery saved, or MAGPIE_NO_ACCOUNTS set to anything
// but "", "0" or "false" for this process alone. The variable only ever
// turns discovery off; it never turns on what the user saved off.
func AccountDiscoveryOff() bool {
	if noAccountsEnv() {
		return true
	}
	fileMu.RLock()
	defer fileMu.RUnlock()
	// the one setting, parsed once for each version of the file: it is
	// asked for each agent and each plugin every time the providers are
	// built, a whole parse of settings.json each (#2502)
	off, err := filememo.Read("settings:no-accounts", Path(), func(b []byte) (bool, error) {
		s, err := parse(b)
		return s.NoAccountDiscovery, err
	})
	if err != nil {
		return load().NoAccountDiscovery
	}
	return off
}

// AccountDiscoveryOffIn is AccountDiscoveryOff with the settings already
// read, for a caller asking it many times in one look.
func AccountDiscoveryOffIn(s Settings) bool {
	return noAccountsEnv() || s.NoAccountDiscovery
}

func noAccountsEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("MAGPIE_NO_ACCOUNTS")))
	return v != "" && v != "0" && v != "false"
}

// AntigravityBuiltin is whether magpie's own Antigravity subscription
// serves its accounts again on this computer: Google's Code Assist asked
// directly with Antigravity's OAuth client, as a native provider owns its
// sign-in, project, translation and account pool (AntigravityBuiltin saved,
// or MAGPIE_ANTIGRAVITY_BUILTIN set to anything but "", "0" or "false").
// Off, as by default, the built-in stays retired (provider.BuiltinRetired):
// Google may suspend an account it sees used outside Antigravity, which is
// the risk the user takes by turning it on.
func AntigravityBuiltin() bool {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("MAGPIE_ANTIGRAVITY_BUILTIN"))); v != "" {
		return v != "0" && v != "false"
	}
	fileMu.RLock()
	defer fileMu.RUnlock()
	// asked wherever a retired built-in is, every time the providers are
	// built: parsed once for each version of the file, as discovery is
	on, err := filememo.Read("settings:antigravity-builtin", Path(), func(b []byte) (bool, error) {
		s, err := parse(b)
		return s.AntigravityBuiltin, err
	})
	if err != nil {
		// unknown is off: a file that doesn't parse turns nothing on
		return load().AntigravityBuiltin
	}
	return on
}
