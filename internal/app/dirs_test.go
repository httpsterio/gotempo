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
