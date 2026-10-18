package supervisor

import (
	"log/slog"
	"strings"

	"github.com/zrougamed/nyxd/internal/apparmor"
)

// resolveApparmorProfile returns a profile name safe to put in the OCI spec.
// When the host has no AppArmor (SELinux distros, Alpine, non-Linux builds),
// the profile is dropped so crun does not fail on an unsupported LSM.
// When AppArmor is present but the named profile is not loaded, it is also
// dropped with a warning (callers should load profiles before compose up).
func resolveApparmorProfile(profile string, log *slog.Logger) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return ""
	}
	if !apparmor.Available() {
		if log != nil {
			log.Warn("apparmor_profile ignored: AppArmor not available on this host", "profile", profile)
		}
		return ""
	}
	if !apparmor.ProfileLoaded(profile) {
		if log != nil {
			log.Warn("apparmor_profile ignored: profile not loaded on host", "profile", profile)
		}
		return ""
	}
	return profile
}
