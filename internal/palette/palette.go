// Package palette provides bundled colors and Gitbuddy's simple custom themes.
package palette

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Bundled colors are adapted from OpenCode v2.2.7's MIT-licensed theme assets.
// Copyright (c) 2025 opencode. See THIRD_PARTY_NOTICES.md for the MIT notice.
// These are flattened palettes, not an implementation of OpenCode's parser.
//
//go:embed themes/*.json
var bundled embed.FS

// Colors uses opaque #RRGGBB strings. Active is always darker than Panel.
type Colors struct {
	Background string `json:"background"`
	Panel      string `json:"panel"`
	Active     string `json:"active"`
	Selection  string `json:"selection"`
	Text       string `json:"text"`
	Muted      string `json:"muted"`
	Accent     string `json:"accent"`
}

type theme struct {
	dark, light Colors
}

// Catalog retains bundled names first, then valid custom filenames in order.
// Callers serialize access to Resolve and its visible, deduplicated Warnings.
type Catalog struct {
	Names    []string
	Warnings []string
	themes   map[string]theme
}

var filename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var hexRGB = regexp.MustCompile(`^#[a-fA-F0-9]{6}$`)

// Load reads custom JSON files from directory/themes without creating or writing
// anything. Custom themes use {"dark":{...Colors},"light":{...Colors}}; at least
// one complete mode is required, and active must be darker than panel.
func Load(directory string) *Catalog {
	c := &Catalog{themes: make(map[string]theme)}
	for _, name := range []string{"opencode", "tokyonight", "catppuccin"} {
		data, err := bundled.ReadFile("themes/" + name + ".json")
		if err != nil {
			panic(err) // A missing or invalid embedded asset is a build defect.
		}
		colors, err := parse(data)
		if err != nil {
			panic(fmt.Sprintf("bundled theme %s: %v", name, err))
		}
		c.themes[name] = colors
		c.Names = append(c.Names, name)
	}
	path := filepath.Join(directory, "themes")
	entries, err := os.ReadDir(path)
	if err != nil {
		if !os.IsNotExist(err) {
			c.warn(fmt.Sprintf("Cannot read custom themes from %s: %v. Using bundled themes.", path, err))
		}
		return c
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if _, exists := c.themes[name]; exists {
			c.warn(fmt.Sprintf("Custom theme %s conflicts with a bundled theme; bundled %s is retained. Rename the custom file.", entry.Name(), name))
			continue
		}
		var colors theme
		var err error
		if len(name) > 255 || !filename.MatchString(name) || strings.Contains(name, "..") {
			err = errors.New("unsafe theme filename")
		} else {
			var data []byte
			data, err = os.ReadFile(filepath.Join(path, entry.Name()))
			if err == nil {
				colors, err = parse(data)
			}
		}
		if err != nil {
			c.warn(fmt.Sprintf("Cannot load custom theme %s: %v. Selecting it will use opencode instead.", entry.Name(), err))
			continue
		}
		c.themes[name] = colors
		c.Names = append(c.Names, name)
	}
	return c
}

// Resolve falls back to the theme's existing mode, or opencode for unknown names.
// The caller resolves system preference to light/dark; other values use dark.
func (c *Catalog) Resolve(name, mode string) Colors {
	colors, exists := c.themes[name]
	if !exists {
		c.warn(fmt.Sprintf("Theme %q is unavailable; using opencode instead.", name))
		colors = c.themes["opencode"]
	}
	if mode == "light" {
		return colors.light
	}
	return colors.dark
}

func (c *Catalog) warn(message string) {
	for _, warning := range c.Warnings {
		if warning == message {
			return
		}
	}
	c.Warnings = append(c.Warnings, message)
}

func parse(data []byte) (theme, error) {
	var modes map[string]json.RawMessage
	if err := json.Unmarshal(data, &modes); err != nil {
		return theme{}, err
	}
	if len(modes) == 0 {
		return theme{}, errors.New("theme requires at least one complete dark or light mode")
	}
	for _, key := range []string{"$schema", "base", "theme", "defs", "hue"} {
		if _, exists := modes[key]; exists {
			return theme{}, errors.New("OpenCode-formatted themes require conversion to Gitbuddy's simple dark/light color schema")
		}
	}
	var result theme
	for mode, raw := range modes {
		if mode != "light" && mode != "dark" {
			return theme{}, fmt.Errorf("unknown theme field %q", mode)
		}
		colors, err := parseColors(raw)
		if err != nil {
			return theme{}, fmt.Errorf("%s: %w", mode, err)
		}
		if mode == "light" {
			result.light = colors
		} else {
			result.dark = colors
		}
	}
	if result.light == (Colors{}) {
		result.light = result.dark
	}
	if result.dark == (Colors{}) {
		result.dark = result.light
	}
	return result, nil
}

func parseColors(data []byte) (Colors, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Colors{}, err
	}
	var colors Colors
	targets := map[string]*string{
		"background": &colors.Background, "panel": &colors.Panel, "active": &colors.Active,
		"selection": &colors.Selection, "text": &colors.Text, "muted": &colors.Muted, "accent": &colors.Accent,
	}
	for key, raw := range fields {
		target, exists := targets[key]
		if !exists {
			return Colors{}, fmt.Errorf("unknown color %q", key)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return Colors{}, fmt.Errorf("%s must be #RRGGBB", key)
		}
	}
	for _, key := range []string{"background", "panel", "active", "selection", "text", "muted", "accent"} {
		if !hexRGB.MatchString(*targets[key]) {
			return Colors{}, fmt.Errorf("%s must be #RRGGBB", key)
		}
	}
	if brightness(colors.Active) >= brightness(colors.Panel) {
		return Colors{}, errors.New("active must be darker than panel")
	}
	return colors, nil
}

func brightness(hex string) float64 {
	rgb, _ := strconv.ParseUint(hex[1:], 16, 24)
	return float64(rgb>>16)*0.2126 + float64((rgb>>8)&255)*0.7152 + float64(rgb&255)*0.0722
}
