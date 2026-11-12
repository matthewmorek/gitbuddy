import { afterEach, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { ThemeCatalog } from "../src/theme";
import opencode from "../src/themes/opencode.json";
import tokyonight from "../src/themes/tokyonight.json";

import { temporaryRoot } from "./temp";
const owned: string[] = [];
async function directory(): Promise<string> {
  const path = await mkdtemp(join(temporaryRoot, "gitbuddy-themes-"));
  owned.push(path);
  await mkdir(join(path, "themes"));
  return path;
}
async function custom(path: string, name: string, input: unknown): Promise<void> {
  await writeFile(join(path, "themes", `${name}.json`), JSON.stringify(input));
}
afterEach(async () => {
  await Promise.all(owned.splice(0).map((path) => rm(path, { recursive: true, force: true })));
});

test("the three actual presets resolve semantic/diff/syntax/dialog tokens in both modes", async () => {
  const catalog = await ThemeCatalog.load(await directory());
  expect(catalog.names).toEqual(["opencode", "tokyonight", "catppuccin"]);
  expect(catalog.warnings).toEqual([]);
  const colors: Record<string, Record<"dark" | "light", [number, number, number, number]>> = {
    opencode: { dark: [10, 10, 10, 255], light: [255, 255, 255, 255] },
    tokyonight: { dark: [26, 27, 38, 255], light: [225, 226, 231, 255] },
    catppuccin: { dark: [30, 30, 46, 255], light: [239, 241, 245, 255] },
  };
  for (const name of catalog.names) {
    for (const mode of ["dark", "light"] as const) {
      const theme = catalog.resolve(name, mode);
      expect(theme.background.base.toInts()).toEqual(colors[name][mode]);
      expect(theme.text.base.a).toBe(1);
      expect(theme.diff.text.added.a).toBe(1);
      expect(theme.diff.background.removed.a).toBe(1);
      expect(theme.syntax.keyword.a).toBe(1);
      expect(theme.categorical.length).toBeGreaterThan(0);
      expect(theme.surface("dialog").background.base.a).toBe(1);
      expect(theme.text.action.primary.state({ focused: true })).toBe(theme.text.action.primary.focused);
    }
  }
});

test("custom V2 one-mode documents fall back to their existing mode", async () => {
  const path = await directory();
  const { light: _, ...darkOnly } = structuredClone(opencode);
  darkOnly.base.text.base = "#123456";
  await custom(path, "night-only", darkOnly);
  const catalog = await ThemeCatalog.load(path);
  expect(catalog.warnings).toEqual([]);
  expect(catalog.names).toContain("night-only");
  for (const mode of ["light", "dark"] as const) {
    const theme = catalog.resolve("night-only", mode);
    expect(theme.text.base.toInts()).toEqual([18, 52, 86, 255]);
    expect(theme.background.base.toInts()).toEqual([10, 10, 10, 255]);
  }
});

test("custom legacy documents use the published migration with references intact", async () => {
  const path = await directory();
  const legacy = structuredClone(tokyonight);
  legacy.defs.darkStep1 = "#010203";
  legacy.defs.lightStep1 = "#f0f1f2";
  await custom(path, "legacy", legacy);
  const catalog = await ThemeCatalog.load(path);
  expect(catalog.warnings).toEqual([]);
  expect(catalog.resolve("legacy", "dark").background.base.toInts()).toEqual([1, 2, 3, 255]);
  expect(catalog.resolve("legacy", "light").background.base.toInts()).toEqual([240, 241, 242, 255]);
});

test("malformed JSON, incomplete legacy, and invalid secondary modes warn and fall back", async () => {
  const path = await directory();
  await writeFile(join(path, "themes", "broken.json"), "{broken");
  await custom(path, "incomplete", { theme: { primary: "#ffffff" } });
  const invalidMode = structuredClone(opencode);
  Object.assign(invalidMode.dark, { text: { base: "$missing.reference" } });
  await custom(path, "invalid-mode", invalidMode);
  const catalog = await ThemeCatalog.load(path);
  expect(catalog.names).toEqual(["opencode", "tokyonight", "catppuccin"]);
  expect(catalog.warnings).toHaveLength(3);
  for (const name of ["broken", "incomplete", "invalid-mode", "unknown"]) {
    expect(catalog.resolve(name, "dark").background.base.toInts()).toEqual(catalog.resolve("opencode", "dark").background.base.toInts());
    expect(catalog.warnings.some((warning) => warning.includes(name))).toBe(true);
  }
  const warnings = catalog.warnings.length;
  catalog.resolve("unknown", "dark");
  expect(catalog.warnings).toHaveLength(warnings);
});

test("custom files cannot silently replace bundled names", async () => {
  const path = await directory();
  const replacement = structuredClone(opencode);
  replacement.base.background.base = "#123456";
  await custom(path, "opencode", replacement);
  const catalog = await ThemeCatalog.load(path);
  expect(catalog.warnings).toHaveLength(1);
  expect(catalog.warnings[0]).toContain("conflicts");
  expect(catalog.resolve("opencode", "dark").background.base.toInts()).toEqual([10, 10, 10, 255]);
});

test("absent or unreadable custom-theme directories leave bundled themes usable", async () => {
  const path = await directory();
  await rm(join(path, "themes"), { recursive: true });
  const absent = await ThemeCatalog.load(path);
  expect(absent.warnings).toEqual([]);
  await writeFile(join(path, "themes"), "not a directory");
  const invalid = await ThemeCatalog.load(path);
  expect(invalid.warnings).toHaveLength(1);
  expect(invalid.resolve("opencode", "light").background.base.toInts()).toEqual([255, 255, 255, 255]);
});
