import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import { migrateV1, parseThemeDocument, resolveThemeDocument, type ResolvedTheme, type ThemeDocument } from "@opencode/theme/tui";
import type { ThemeV1Json } from "@opencode/theme/tui/v1";
import opencode from "./themes/opencode.json";
import tokyonight from "./themes/tokyonight.json";
import catppuccin from "./themes/catppuccin.json";

function parse(input: unknown, name: string): ThemeDocument {
  const legacy = input !== null && typeof input === "object" && "theme" in input && !("base" in input);
  // migrateV1 performs legacy reference resolution; parse validates all available modes.
  return parseThemeDocument(legacy ? migrateV1(input as ThemeV1Json) : input, name);
}

/** Loads Gitbuddy-only custom themes; bundles and custom files share the same parser. */
export class ThemeCatalog {
  names: string[] = [];
  warnings: string[] = [];
  private themes = new Map<string, ThemeDocument>();

  private constructor() {}

  static async load(directory: string): Promise<ThemeCatalog> {
    const catalog = new ThemeCatalog();
    for (const [name, input] of Object.entries({ opencode, tokyonight, catppuccin })) {
      catalog.themes.set(name, parse(input, name));
    }
    const path = join(directory, "themes");
    let entries: string[];
    try {
      entries = (await readdir(path)).filter((name) => name.endsWith(".json")).sort();
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
        catalog.warn(`Cannot read custom themes from ${path}: ${String(error)}. Using bundled themes.`);
      }
      entries = [];
    }
    for (const entry of entries) {
      const name = entry.slice(0, -5);
      if (catalog.themes.has(name)) {
        catalog.warn(`Custom theme ${entry} conflicts with a bundled theme; the bundled ${name} is retained. Rename the custom file.`);
        continue;
      }
      try {
        if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(name) || name.includes("..")) throw new Error("Unsafe theme filename");
        catalog.themes.set(name, parse(JSON.parse(await readFile(join(path, entry), "utf8")), name));
      } catch (error) {
        catalog.warn(`Cannot load custom theme ${entry}: ${String(error)}. Selecting it will use opencode instead.`);
      }
    }
    catalog.names = [...catalog.themes.keys()];
    return catalog;
  }

  /** Missing modes use the theme's existing mode; unknown/invalid names use opencode. */
  resolve(name: string, mode: "light" | "dark"): ResolvedTheme {
    const theme = this.themes.get(name);
    if (!theme) this.warn(`Theme "${name}" is unavailable; using opencode instead.`);
    return resolveThemeDocument(theme ?? this.themes.get("opencode")!, mode);
  }

  private warn(message: string): void {
    if (!this.warnings.includes(message)) this.warnings.push(message);
  }
}
