package palette

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func custom(t *testing.T, directory, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "themes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "themes", name+".json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestActualBundledColorsInBothModes(t *testing.T) {
	directory := t.TempDir()
	catalog := Load(directory)
	if !reflect.DeepEqual(catalog.Names, []string{"opencode", "tokyonight", "catppuccin"}) || len(catalog.Warnings) != 0 {
		t.Fatalf("unexpected catalog: %+v", catalog)
	}
	// Flattened from src/themes/*.json and src/app.tsx's focus ordering.
	expected := map[string]map[string]Colors{
		"opencode": {
			"dark":  {"#0a0a0a", "#141414", "#0a0a0a", "#1e1e1e", "#eeeeee", "#808080", "#9d7cd8"},
			"light": {"#ffffff", "#ffffff", "#fafafa", "#f5f5f5", "#1a1a1a", "#8a8a8a", "#d68c27"},
		},
		"tokyonight": {
			"dark":  {"#1a1b26", "#1e2030", "#1a1b26", "#222436", "#c8d3f5", "#828bb8", "#ff966c"},
			"light": {"#e1e2e7", "#e1e2e7", "#d5d6db", "#c8c9ce", "#3760bf", "#8990a3", "#b15c00"},
		},
		"catppuccin": {
			"dark":  {"#1e1e2e", "#1e1e2e", "#181825", "#11111b", "#cdd6f4", "#9399b2", "#f5c2e7"},
			"light": {"#eff1f5", "#eff1f5", "#e6e9ef", "#dce0e8", "#4c4f69", "#7c7f93", "#ea76cb"},
		},
	}
	for name, modes := range expected {
		for mode, want := range modes {
			got := catalog.Resolve(name, mode)
			if got != want {
				t.Errorf("%s/%s = %+v; want %+v", name, mode, got, want)
			}
			if brightness(got.Active) >= brightness(got.Panel) {
				t.Errorf("%s/%s active panel is not darker", name, mode)
			}
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("load wrote files: %v, %v", entries, err)
	}
}

func TestCustomModesAndMissingModeFallback(t *testing.T) {
	directory := t.TempDir()
	dark := Colors{"#010203", "#223344", "#112233", "#445566", "#FFFFFF", "#aabbcc", "#abcdef"}
	light := Colors{"#f0f1f2", "#ffffff", "#dddddd", "#cccccc", "#010203", "#445566", "#123456"}
	for name, modes := range map[string]map[string]Colors{
		"a-dark-only": {"dark": dark}, "b-light-only": {"light": light}, "both": {"dark": dark, "light": light},
	} {
		data, err := json.Marshal(modes)
		if err != nil {
			t.Fatal(err)
		}
		custom(t, directory, name, string(data))
	}
	catalog := Load(directory)
	if len(catalog.Warnings) != 0 || !reflect.DeepEqual(catalog.Names, []string{"opencode", "tokyonight", "catppuccin", "a-dark-only", "b-light-only", "both"}) {
		t.Fatalf("unexpected custom catalog: %+v", catalog)
	}
	for _, mode := range []string{"dark", "light"} {
		if got := catalog.Resolve("a-dark-only", mode); got != dark {
			t.Errorf("dark-only/%s = %+v", mode, got)
		}
		if got := catalog.Resolve("b-light-only", mode); got != light {
			t.Errorf("light-only/%s = %+v", mode, got)
		}
	}
	if catalog.Resolve("both", "dark") != dark || catalog.Resolve("both", "light") != light {
		t.Fatal("mode selection ignored")
	}
}

func TestInvalidCustomThemesWarnAndFallbackWithoutWriting(t *testing.T) {
	directory := t.TempDir()
	valid := `{"dark":{"background":"#010203","panel":"#222222","active":"#111111","selection":"#333333","text":"#ffffff","muted":"#888888","accent":"#ff0000"}}`
	files := map[string]string{
		"broken": "{broken", "array": "[]", "null": "null", "empty": "{}",
		"incomplete": `{"dark":{"background":"#010203"}}`,
		"null-mode":  `{"dark":null}`, "unknown-mode": `{"sepia":{}}`,
		"invalid-secondary": strings.TrimSuffix(valid, "}") + `,"light":{"text":"invalid"}}`,
		"unknown-color":     strings.Replace(valid, `"background"`, `"Background"`, 1),
		"bad-hex":           strings.Replace(valid, "#010203", "#fff", 1),
		"alpha":             strings.Replace(valid, "#010203", "#010203ff", 1),
		"bad-contrast":      strings.Replace(valid, "#111111", "#ffffff", 1),
		"flat-black":        strings.ReplaceAll(strings.ReplaceAll(valid, "#111111", "#000000"), "#222222", "#000000"),
		"a..b":              valid,
		"opencode-v1":       `{"defs":{},"theme":{"background":"#010203"}}`,
		"opencode-v2":       `{"base":{},"dark":{"hue":{}}}`,
	}
	for name, data := range files {
		custom(t, directory, name, data)
	}
	catalog := Load(directory)
	if len(catalog.Names) != 3 || len(catalog.Warnings) != len(files) {
		t.Fatalf("invalid files accepted or not warned: %+v", catalog)
	}
	for name, original := range files {
		if got := catalog.Resolve(name, "light"); got != catalog.Resolve("opencode", "light") {
			t.Errorf("%s did not fall back", name)
		}
		data, err := os.ReadFile(filepath.Join(directory, "themes", name+".json"))
		if err != nil || string(data) != original {
			t.Errorf("custom %s modified: %q, %v", name, data, err)
		}
	}
	for _, name := range []string{"opencode-v1", "opencode-v2"} {
		found := false
		for _, warning := range catalog.Warnings {
			if strings.Contains(warning, name) && strings.Contains(warning, "require conversion") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s lacked conversion warning", name)
		}
	}
	catalog.Resolve("missing", "dark")
	count := len(catalog.Warnings)
	catalog.Resolve("missing", "dark")
	if len(catalog.Warnings) != count {
		t.Fatal("repeated resolve duplicated warnings")
	}
}

func TestCustomCannotReplaceBundledTheme(t *testing.T) {
	directory := t.TempDir()
	original := `{"dark":{"background":"#010203"}}`
	custom(t, directory, "opencode", original)
	catalog := Load(directory)
	if len(catalog.Warnings) != 1 || !strings.Contains(catalog.Warnings[0], "conflicts") || catalog.Resolve("opencode", "dark").Background != "#0a0a0a" {
		t.Fatalf("collision mishandled: %+v", catalog)
	}
	data, err := os.ReadFile(filepath.Join(directory, "themes", "opencode.json"))
	if err != nil || string(data) != original {
		t.Fatal("conflicting file changed")
	}
}

func TestUnreadableThemesDirectoryStillProvidesBuiltins(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "themes"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog := Load(directory)
	if len(catalog.Warnings) != 1 || catalog.Resolve("opencode", "light").Background != "#ffffff" {
		t.Fatalf("unreadable themes disabled builtins: %+v", catalog)
	}
}
