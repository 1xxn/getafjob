package app

import (
	"strings"
	"testing"
)

func TestAdminMaintenanceUsage(t *testing.T) {
	for _, args := range [][]string{
		{"maintenance"}, {"maintenance", "pause"}, {"maintenance", "off", "-message", "x"}, {"maintenance", "on", "extra"},
	} {
		env, _, errb := testEnv(nil)
		if code := Main(t.Context(), append([]string{"admin"}, args...), env); code != ExitUsage ||
			!strings.Contains(errb.String(), "maintenance on") {
			t.Errorf("%v: code %d, stderr %q", args, code, errb.String())
		}
	}
}
