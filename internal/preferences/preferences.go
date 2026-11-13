// Package preferences owns only Gitbuddy's config.json, never OpenCode's settings.
package preferences

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
)

// Settings retains the JSON schema used by earlier Gitbuddy releases.
type Settings struct {
	AutoFetch            bool   `json:"autoFetch"`
	FetchIntervalMinutes int    `json:"fetchIntervalMinutes"`
	MaxCommits           int    `json:"maxCommits"`
	Theme                string `json:"theme"`
	ColorMode            string `json:"colorMode"`
}

// Defaults returns independent, current defaults for missing preferences.
func Defaults() Settings {
	return Settings{true, 5, 100, "opencode", "system"}
}

var themeFilename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Validate rejects settings that cannot safely be used or round-tripped to JSON.
func Validate(value Settings) error {
	if value.FetchIntervalMinutes < 1 || value.FetchIntervalMinutes > 1440 {
		return errors.New("fetchIntervalMinutes must be an integer from 1 to 1440")
	}
	if value.MaxCommits < 1 || uint64(value.MaxCommits) > 9007199254740991 {
		return errors.New("maxCommits must be a positive safe integer")
	}
	if len(value.Theme) > 255 || !themeFilename.MatchString(value.Theme) || strings.Contains(value.Theme, "..") {
		return errors.New("theme must be a nonempty filename without paths or traversal")
	}
	if value.ColorMode != "system" && value.ColorMode != "light" && value.ColorMode != "dark" {
		return errors.New("colorMode must be system, light, or dark")
	}
	return nil
}

func parse(data []byte) (Settings, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Settings{}, err
	}
	if fields == nil {
		return Settings{}, errors.New("settings must be a JSON object")
	}
	value := Defaults()
	targets := map[string]any{
		"autoFetch": &value.AutoFetch, "fetchIntervalMinutes": &value.FetchIntervalMinutes,
		"maxCommits": &value.MaxCommits, "theme": &value.Theme, "colorMode": &value.ColorMode,
	}
	for key, raw := range fields {
		target, known := targets[key]
		if !known {
			return Settings{}, fmt.Errorf("unknown setting %q; this file may require a newer Gitbuddy", key)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Settings{}, fmt.Errorf("%s must not be null", key)
		}
		if integer, ok := target.(*int); ok {
			// Match the previous JSON number semantics: 5.0 and 1e2 are
			// integers too, but strings, fractions and unsafe values are not.
			var number float64
			if err := json.Unmarshal(raw, &number); err != nil || number < 1 ||
				number > 9007199254740991 || math.Trunc(number) != number {
				return Settings{}, fmt.Errorf("%s must be a positive safe integer", key)
			}
			*integer = int(number)
			continue
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return Settings{}, fmt.Errorf("%s: %w", key, err)
		}
	}
	return value, Validate(value)
}

type snapshot struct {
	data []byte
	info os.FileInfo
}

func sameInfo(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime() == b.ModTime() &&
		a.Mode() == b.Mode() && changeTime(a) == changeTime(b)
}

func unchanged(a, b *snapshot) bool {
	if a == nil || b == nil {
		return a == b
	}
	return sameInfo(a.info, b.info) && bytes.Equal(a.data, b.data)
}

// Check the entry and opened descriptor: a swapped-in symlink is never followed.
func readConfig(path string) (*snapshot, error) {
	entry, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !entry.Mode().IsRegular() {
		return nil, errors.New("config.json must be a regular file, not a symlink")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || !sameInfo(entry, before) {
		return nil, errors.New("config.json changed while being read")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	current, err := os.Lstat(path)
	if err != nil || !sameInfo(before, after) || !sameInfo(after, current) {
		return nil, errors.New("config.json changed while being read")
	}
	return &snapshot{data, after}, nil
}

// Store applies valid updates even when saving fails. Callers own serialized UI
// reads of Value and Warning; the mutex serializes concurrent Update calls.
// Directory is informational and must not be changed after Load.
type Store struct {
	Value     Settings
	Warning   string
	Directory string
	mu        sync.Mutex
	path      string
	original  *snapshot
	blocked   error
}

// Load is read-only. Invalid or unsafe initial files keep defaults and block
// writes until repaired externally and loaded again. An empty directory uses XDG.
func Load(directory string) *Store {
	store := &Store{Value: Defaults()}
	if directory == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				store.blocked = err
				store.Warning = fmt.Sprintf("Cannot locate Gitbuddy settings: %v; saving is blocked", err)
				return store
			}
			base = filepath.Join(home, ".config")
		}
		directory = filepath.Join(base, "gitbuddy")
	}
	abs, err := filepath.Abs(directory)
	if err == nil {
		store.Directory = abs
		store.path = filepath.Join(abs, "config.json")
		store.original, err = readConfig(store.path)
		if err == nil && store.original != nil {
			var value Settings
			value, err = parse(store.original.data)
			if err == nil {
				store.Value = value
			}
		}
	}
	if err != nil {
		store.blocked = fmt.Errorf("cannot safely use %s: %w; repair externally and reload Gitbuddy; saving is blocked", store.path, err)
		store.Warning = store.blocked.Error()
	}
	return store
}

// Update validates the complete next value, applies it for this session, then
// saves atomically with private permissions. Invalid values do not apply.
func (s *Store) Update(next Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := Validate(next); err != nil {
		return err
	}
	s.Value = next
	if err := s.save(next); err != nil {
		s.Warning = fmt.Sprintf("Settings applied for this session but could not be saved to %s: %v. Check permissions and retry (reload after external repairs).", s.path, err)
		return errors.New(s.Warning)
	}
	s.Warning = ""
	return nil
}

func (s *Store) save(value Settings) error {
	if s.blocked != nil {
		return s.blocked
	}
	if err := os.MkdirAll(s.Directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(s.Directory, ".config.json.*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		file.Close()
		if temporary != "" {
			os.Remove(temporary) // Only our own temporary, never a foreign file.
		}
	}()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	current, err := readConfig(s.path)
	if err != nil {
		s.blocked = fmt.Errorf("cannot safely replace %s: %w; repair externally and reload Gitbuddy", s.path, err)
		return s.blocked
	}
	if !unchanged(s.original, current) {
		s.blocked = fmt.Errorf("%s changed outside Gitbuddy; reload before saving to preserve those edits", s.path)
		return s.blocked
	}
	// No portable filesystem compare-and-swap exists. As in the prior store,
	// external writers must not edit between this final comparison and rename.
	if err := os.Rename(temporary, s.path); err != nil {
		return err
	}
	temporary = ""
	// Keep our descriptor's identity, not an external replacement read by path.
	info, err := file.Stat()
	if err != nil {
		s.blocked = fmt.Errorf("cannot confirm saved settings: %w; reload before saving", err)
		return s.blocked
	}
	s.original = &snapshot{data, info}
	if err := file.Close(); err != nil {
		return err
	}
	directory, err := os.Open(s.Directory)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
