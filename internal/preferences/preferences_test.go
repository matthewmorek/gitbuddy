package preferences

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func writeConfig(t *testing.T, directory, data string) string {
	t.Helper()
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertBytes(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != expected {
		t.Fatalf("read %s = %q, %v; want %q", path, data, err, expected)
	}
}

func assertNoTemporaries(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config.json.") {
			t.Fatalf("temporary was not cleaned up: %s", entry.Name())
		}
	}
}

func TestDefaultsReadOnlyAndPrivateRoundTrip(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "nested")
	store := Load(directory)
	if store.Directory != directory || store.Value != Defaults() || store.Warning != "" {
		t.Fatalf("unexpected initial store: %+v", store)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("load wrote settings: %v", err)
	}
	next := Settings{false, 1440, 9007199254740991, "my.theme-1", "light"}
	if err := store.Update(next); err != nil {
		t.Fatal(err)
	}
	if got := Load(directory); got.Value != next || got.Warning != "" {
		t.Fatalf("round trip: %+v", got)
	}
	info, err := os.Stat(filepath.Join(directory, "config.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings permissions: %v, %v", info, err)
	}
	assertNoTemporaries(t, directory)
}

func TestDefaultDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	if got := Load(""); got.Directory != filepath.Join(root, "gitbuddy") {
		t.Fatalf("XDG directory = %q", got.Directory)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", root)
	if got := Load(""); got.Directory != filepath.Join(root, ".config", "gitbuddy") {
		t.Fatalf("home directory = %q", got.Directory)
	}
}

func TestOldSettingsMergeDefaultsWithoutRewriting(t *testing.T) {
	directory := t.TempDir()
	original := "{\"autoFetch\":false,\"fetchIntervalMinutes\":15,\"theme\":\"tokyonight\",\"colorMode\":\"dark\"}\n"
	path := writeConfig(t, directory, original)
	store := Load(directory)
	expected := Settings{false, 15, 100, "tokyonight", "dark"}
	if store.Value != expected || store.Warning != "" {
		t.Fatalf("old config: %+v", store)
	}
	assertBytes(t, path, original)
	expected.MaxCommits = 1
	if err := store.Update(expected); err != nil {
		t.Fatal(err)
	}
	if got := Load(directory).Value; got != expected {
		t.Fatalf("saved settings = %+v; want %+v", got, expected)
	}
}

func TestMalformedSettingsPreservedAndBlocked(t *testing.T) {
	for _, original := range []string{
		"{broken", "[]", "null", "{} {}", `{"autoFetch":false,"version":2}`,
		`{"AutoFetch":false}`, `{"autoFetch":"false"}`, `{"autoFetch":null}`,
		`{"fetchIntervalMinutes":0}`, `{"fetchIntervalMinutes":1441}`, `{"fetchIntervalMinutes":1.5}`,
		`{"maxCommits":0}`, `{"maxCommits":-1}`, `{"maxCommits":1.5}`,
		`{"maxCommits":9007199254740992}`, `{"maxCommits":900719925474099100}`,
		`{"maxCommits":"100"}`, `{"maxCommits":null}`, `{"colorMode":false}`,
		`{"colorMode":"sepia"}`, `{"theme":"../outside"}`, `{"theme":"a\\b"}`,
	} {
		t.Run(original, func(t *testing.T) {
			directory := t.TempDir()
			path := writeConfig(t, directory, original)
			store := Load(directory)
			if store.Value != Defaults() || store.Warning == "" {
				t.Fatalf("invalid config accepted: %+v", store)
			}
			next := Defaults()
			next.AutoFetch = false
			if err := store.Update(next); err == nil || !strings.Contains(err.Error(), "saving is blocked") {
				t.Fatalf("update should be blocked: %v", err)
			}
			if store.Value != next {
				t.Fatal("valid session change not applied")
			}
			assertBytes(t, path, original)
			// Repairs cannot silently unblock this store; an explicit reload is needed.
			writeConfig(t, directory, "{}")
			if err := store.Update(next); err == nil {
				t.Fatal("repair bypassed reload requirement")
			}
			assertBytes(t, path, "{}")
			assertNoTemporaries(t, directory)
		})
	}
}

func TestIntegerJSONNumberCompatibility(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, directory, `{"fetchIntervalMinutes":5.0,"maxCommits":1e2}`)
	store := Load(directory)
	if store.Warning != "" || store.Value != Defaults() {
		t.Fatalf("valid integer JSON numbers rejected: %+v", store)
	}
}

func TestInvalidRuntimeSettingsNeitherApplyNorSave(t *testing.T) {
	directory := t.TempDir()
	path := writeConfig(t, directory, "{}")
	store := Load(directory)
	for _, mutate := range []func(*Settings){
		func(v *Settings) { v.FetchIntervalMinutes = 0 },
		func(v *Settings) { v.FetchIntervalMinutes = 1441 },
		func(v *Settings) { v.MaxCommits = 0 },
		func(v *Settings) { v.MaxCommits = 9007199254740992 },
		func(v *Settings) { v.Theme = "" },
		func(v *Settings) { v.Theme = "a/b" },
		func(v *Settings) { v.Theme = "a..b" },
		func(v *Settings) { v.Theme = strings.Repeat("a", 256) },
		func(v *Settings) { v.ColorMode = "sepia" },
	} {
		next := Defaults()
		mutate(&next)
		if err := store.Update(next); err == nil {
			t.Fatalf("accepted invalid settings: %+v", next)
		}
		if store.Value != Defaults() || store.Warning != "" {
			t.Fatal("invalid update changed session state")
		}
	}
	assertBytes(t, path, "{}")
}

func TestFailedPersistenceAppliesAndRetries(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "settings")
	store := Load(directory)
	if err := os.WriteFile(directory, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	next := Defaults()
	next.ColorMode = "light"
	if err := store.Update(next); err == nil || !strings.Contains(err.Error(), "could not be saved") {
		t.Fatalf("expected persistence failure: %v", err)
	}
	if store.Value != next || store.Warning == "" {
		t.Fatal("session settings were lost")
	}
	assertBytes(t, directory, "not a directory")
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	next.Theme = "catppuccin"
	if err := store.Update(next); err != nil {
		t.Fatal(err)
	}
	if store.Warning != "" || Load(directory).Value != next {
		t.Fatal("retry failed")
	}
	assertNoTemporaries(t, directory)
}

func TestExternalChangesBlockUntilReload(t *testing.T) {
	for _, change := range []string{"bytes", "identity", "creation", "deletion", "competing-store"} {
		t.Run(change, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "config.json")
			if change != "creation" {
				writeConfig(t, directory, "{}")
			}
			store := Load(directory)
			expected := "{}"
			switch change {
			case "bytes":
				expected = "{ \"theme\": \"tokyonight\" }\n"
				writeConfig(t, directory, expected)
			case "identity":
				replacement := filepath.Join(directory, "external")
				if err := os.WriteFile(replacement, []byte(expected), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, path); err != nil {
					t.Fatal(err)
				}
			case "creation":
				writeConfig(t, directory, expected)
			case "deletion":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "competing-store":
				other := Load(directory)
				next := Defaults()
				next.Theme = "tokyonight"
				if err := other.Update(next); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				expected = string(data)
			}
			next := Defaults()
			next.ColorMode = "dark"
			for range 2 {
				if err := store.Update(next); err == nil || !strings.Contains(err.Error(), "changed outside Gitbuddy") {
					t.Fatalf("external change not blocked: %v", err)
				}
			}
			if store.Value != next {
				t.Fatal("session change not retained")
			}
			if change == "deletion" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("deleted config recreated: %v", err)
				}
			} else {
				assertBytes(t, path, expected)
			}
			assertNoTemporaries(t, directory)
			reloaded := Load(directory)
			next = reloaded.Value
			next.ColorMode = "light"
			if err := reloaded.Update(next); err != nil {
				t.Fatalf("reload did not permit save: %v", err)
			}
		})
	}
}

func TestUnsafeConfigAtLoadAndBeforeSave(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "config.json")
			before := Load(directory)
			target := filepath.Join(directory, "unrelated.json")
			if err := os.WriteFile(target, []byte(`{"autoFetch":false}`), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(target, path)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			loaded := Load(directory)
			if loaded.Value != Defaults() || loaded.Warning == "" {
				t.Fatal("unsafe config accepted")
			}
			for _, store := range []*Store{before, loaded} {
				if err := store.Update(Defaults()); err == nil {
					t.Fatal("unsafe config overwritten")
				}
			}
			assertBytes(t, target, `{"autoFetch":false}`)
			assertNoTemporaries(t, directory)
		})
	}
}

func TestConcurrentUpdatesAreSerialized(t *testing.T) {
	directory := t.TempDir()
	store := Load(directory)
	var wg sync.WaitGroup
	for i := 1; i <= 8; i++ {
		wg.Go(func() {
			next := Defaults()
			next.MaxCommits = i
			if err := store.Update(next); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if loaded := Load(directory); loaded.Value != store.Value || loaded.Warning != "" {
		t.Fatalf("disk and session differ: %+v / %+v", loaded, store)
	}
	assertNoTemporaries(t, directory)
}
