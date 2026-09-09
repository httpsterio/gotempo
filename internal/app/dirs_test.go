package app

import (
	"path/filepath"
	"testing"
)

// The env var wins when set, which is what lets tests and XDG relocate a
// directory without touching the home dir.
func TestResolveEnvOverride(t *testing.T) {
	t.Setenv("GOTEMPO_TEST_DIR", "/somewhere/else")
	got := resolve("GOTEMPO_TEST_DIR", []string{".local", "share"})
	if want := filepath.Join("/somewhere/else", appName); got != want {
		t.Errorf("resolve = %q, want %q", got, want)
	}
}

// An empty or unset env var falls back to the home-relative path. Empty must not
// resolve to "/gotempo".
func TestResolveFallsBackToHome(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	want := filepath.Join("/home/tester", ".local", "share", appName)

	for _, env := range []string{"", "GOTEMPO_TEST_UNSET"} {
		t.Setenv("GOTEMPO_TEST_UNSET", "")
		if got := resolve(env, []string{".local", "share"}); got != want {
			t.Errorf("resolve(%q) = %q, want %q", env, got, want)
		}
	}
}

// A platform may point config and data at the same folder (Windows does). The
// shared paths built on top must stay distinct files inside it.
func TestSameConfigAndDataDir(t *testing.T) {
	t.Setenv("GOTEMPO_TEST_BOTH", "/base")
	one := dirLayout{
		configEnv: "GOTEMPO_TEST_BOTH", configRel: []string{"AppData", "Local"},
		dataEnv: "GOTEMPO_TEST_BOTH", dataRel: []string{"AppData", "Local"},
	}
	c := resolve(one.configEnv, one.configRel)
	d := resolve(one.dataEnv, one.dataRel)
	if c != d {
		t.Fatalf("config %q and data %q should be the same folder", c, d)
	}
	if filepath.Join(c, "config.json") == filepath.Join(d, "status.json") {
		t.Error("distinct files collided")
	}
}

// The live layout must produce absolute, distinct-per-purpose paths on the
// platform being built. Guards against a platform file with a zero-value dirs.
func TestDirsAreConfigured(t *testing.T) {
	if dirs.configRel == nil || dirs.dataRel == nil {
		t.Fatal("dirs is not populated by the platform file")
	}
	if configDir() == "" || dataDir() == "" {
		t.Fatal("configDir/dataDir resolved to empty")
	}
}

// The first strap keeps the original OBS filename, so a source pointed at it
// keeps working when two-player mode is switched on. The ITGmania files live in
// the module's own subfolder, one per slot.
func TestSlotPathsKeepP1Stable(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")

	if got, want := outputPath(slotP1), filepath.Join("/data", appName, "gotempo-bpm.txt"); got != want {
		t.Errorf("P1 output = %q, want %q", got, want)
	}
	if got, want := outputPath(slotP2), filepath.Join("/data", appName, "gotempo-bpm-p2.txt"); got != want {
		t.Errorf("P2 output = %q, want %q", got, want)
	}

	module := filepath.Join("/themes", "Simply Love", "Modules", "gotempo.lua")
	data := filepath.Join(filepath.Dir(module), "gotempo")
	if got, want := itgHRPathFor(module, slotP1), filepath.Join(data, "hr.txt"); got != want {
		t.Errorf("P1 overlay = %q, want %q", got, want)
	}
	// Deliberately hr-p2.txt and not hr-p1.txt/hr-p2.txt: an already-installed
	// module reads hr.txt and must keep working untouched.
	if got, want := itgHRPathFor(module, slotP2), filepath.Join(data, "hr-p2.txt"); got != want {
		t.Errorf("P2 overlay = %q, want %q", got, want)
	}
}
