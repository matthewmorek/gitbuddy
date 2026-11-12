import { afterEach, expect, test } from "bun:test";
import { lstat, mkdtemp, readFile, readdir, rm, stat, symlink, unlink, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { DEFAULT_SETTINGS, SettingsStore, type Settings } from "../src/settings";

import { temporaryRoot } from "./temp";
const owned: string[] = [];
async function directory(): Promise<string> {
  const path = await mkdtemp(join(temporaryRoot, "gitbuddy-settings-"));
  owned.push(path);
  return path;
}
afterEach(async () => {
  await Promise.all(owned.splice(0).map((path) => rm(path, { recursive: true, force: true })));
});

test("defaults are read-only on load; writes round-trip with private permissions", async () => {
  const root = await directory();
  const path = join(root, "nested");
  const store = await SettingsStore.load(path);
  expect(store.directory).toBe(path);
  expect(store.value).toEqual(DEFAULT_SETTINGS);
  expect(store.value.maxCommits).toBe(100);
  expect(store.warning).toBeNull();
  expect(await readdir(root)).toEqual([]);
  await store.update({ autoFetch: false, fetchIntervalMinutes: 1440, maxCommits: Number.MAX_SAFE_INTEGER, theme: "my.theme-1", colorMode: "light" });
  expect((await SettingsStore.load(path)).value).toEqual(store.value);
  expect((await stat(join(path, "config.json"))).mode & 0o777).toBe(0o600);
  expect(await readdir(path)).toEqual(["config.json"]);
});

test("default directory uses XDG_CONFIG_HOME and never project config", async () => {
  const root = await directory();
  const original = process.env.XDG_CONFIG_HOME;
  process.env.XDG_CONFIG_HOME = root;
  try {
    const store = await SettingsStore.load();
    expect(store.directory).toBe(join(root, "gitbuddy"));
    await store.update({ colorMode: "dark" });
    expect(JSON.parse(await readFile(join(root, "gitbuddy", "config.json"), "utf8")).colorMode).toBe("dark");
  } finally {
    if (original === undefined) delete process.env.XDG_CONFIG_HOME;
    else process.env.XDG_CONFIG_HOME = original;
  }
});

test.each([
  ["malformed JSON", "{broken"],
  ["non-object", "[]"],
  ["unknown/newer preferences", '{"autoFetch":false,"version":2,"extraPreference":"keep"}'],
  ["wrong boolean type", '{"autoFetch":"false"}'],
  ["invalid interval", '{"fetchIntervalMinutes":0}'],
  ["zero commit cap", '{"maxCommits":0}'],
  ["fractional commit cap", '{"maxCommits":1.5}'],
  ["unsafe commit cap", '{"maxCommits":9007199254740992}'],
  ["wrong commit cap type", '{"maxCommits":"100"}'],
  ["null commit cap", '{"maxCommits":null}'],
  ["invalid color mode", '{"colorMode":false}'],
  ["unsafe theme", '{"theme":"../outside"}'],
])("%s is preserved and blocks persistence", async (_name, original) => {
  const path = await directory();
  const config = join(path, "config.json");
  await writeFile(config, original);
  const store = await SettingsStore.load(path);
  expect(store.value).toEqual(DEFAULT_SETTINGS);
  expect(store.warning).not.toBeNull();
  await expect(store.update({ autoFetch: false })).rejects.toThrow("saving is blocked");
  expect(store.value.autoFetch).toBe(false);
  expect(await readFile(config, "utf8")).toBe(original);
});

test("partial known config merges defaults; invalid runtime patches neither apply nor save", async () => {
  const path = await directory();
  await writeFile(join(path, "config.json"), '{"autoFetch":false}');
  const store = await SettingsStore.load(path);
  expect(store.value).toEqual({ ...DEFAULT_SETTINGS, autoFetch: false });
  const invalid: unknown[] = [
    { autoFetch: 1 }, { fetchIntervalMinutes: 1441 }, { fetchIntervalMinutes: 1.5 },
    { maxCommits: -1 }, { maxCommits: 0 }, { maxCommits: 1.5 }, { maxCommits: NaN },
    { maxCommits: Infinity }, { maxCommits: Number.MAX_SAFE_INTEGER + 1 }, { maxCommits: "100" },
    { theme: "" }, { theme: "a/b" }, { theme: "a\\b" }, { theme: "a..b" },
    { colorMode: "sepia" }, { other: true }, { theme: null },
  ];
  for (const patch of invalid) {
    await expect(store.update(patch as Partial<Settings>)).rejects.toThrow();
  }
  expect(store.value).toEqual({ ...DEFAULT_SETTINGS, autoFetch: false });
  expect(await readFile(join(path, "config.json"), "utf8")).toBe('{"autoFetch":false}');
  await store.update({ fetchIntervalMinutes: 1 });
  expect((await SettingsStore.load(path)).value.fetchIntervalMinutes).toBe(1);
});

test("pre-cap config retains existing preferences and merges the cap without rewriting", async () => {
  const path = await directory();
  const config = join(path, "config.json");
  const original = '{"autoFetch":false,"fetchIntervalMinutes":15,"theme":"tokyonight","colorMode":"dark"}\n';
  await writeFile(config, original);
  const store = await SettingsStore.load(path);
  expect(store.warning).toBeNull();
  expect(store.value).toEqual({ autoFetch: false, fetchIntervalMinutes: 15, maxCommits: 100, theme: "tokyonight", colorMode: "dark" });
  expect(await readFile(config, "utf8")).toBe(original);
  await store.update({ maxCommits: 1 });
  expect((await SettingsStore.load(path)).value).toEqual({ ...store.value, maxCommits: 1 });
});

test("failed writes retain immediate changes and can be retried", async () => {
  const root = await directory();
  const path = join(root, "settings");
  const store = await SettingsStore.load(path);
  await writeFile(path, "not a directory");
  const saving = store.update({ colorMode: "light" });
  expect(store.value.colorMode).toBe("light");
  await expect(saving).rejects.toThrow("could not be saved");
  expect(store.warning).toContain("Settings applied for this session");
  expect(await readFile(path, "utf8")).toBe("not a directory");
  await unlink(path);
  await store.update({ theme: "catppuccin" });
  expect(store.warning).toBeNull();
  expect((await SettingsStore.load(path)).value).toEqual({ ...DEFAULT_SETTINGS, colorMode: "light", theme: "catppuccin" });
});

test("queued writes preserve call order and the newest complete value", async () => {
  const path = await directory();
  const store = await SettingsStore.load(path);
  const writes = [
    store.update({ autoFetch: false }),
    store.update({ fetchIntervalMinutes: 15 }),
    store.update({ theme: "tokyonight" }),
    store.update({ colorMode: "dark", fetchIntervalMinutes: 2 }),
  ];
  expect(store.value).toEqual({ autoFetch: false, fetchIntervalMinutes: 2, maxCommits: 100, theme: "tokyonight", colorMode: "dark" });
  await Promise.all(writes);
  expect((await SettingsStore.load(path)).value).toEqual(store.value);
  expect(store.warning).toBeNull();
  expect(await readdir(path)).toEqual(["config.json"]);
});

test("external byte edits block replacement and reload safely adopts them", async () => {
  const path = await directory();
  const config = join(path, "config.json");
  await writeFile(config, '{"autoFetch":false}');
  const store = await SettingsStore.load(path);
  const external = '{ "autoFetch": true, "theme": "tokyonight" }\n';
  await writeFile(config, external);
  await expect(store.update({ colorMode: "dark" })).rejects.toThrow("changed outside Gitbuddy");
  expect(store.value.colorMode).toBe("dark");
  expect(await readFile(config, "utf8")).toBe(external);
  expect(await readdir(path)).toEqual(["config.json"]);
  const reloaded = await SettingsStore.load(path);
  await reloaded.update({ colorMode: "light" });
  expect((await SettingsStore.load(path)).value.theme).toBe("tokyonight");
});

test("a config created or deleted externally is not recreated/overwritten", async () => {
  const path = await directory();
  const config = join(path, "config.json");
  const absent = await SettingsStore.load(path);
  await writeFile(config, "{}");
  await expect(absent.update({ autoFetch: false })).rejects.toThrow("changed outside Gitbuddy");
  expect(await readFile(config, "utf8")).toBe("{}");
  const present = await SettingsStore.load(path);
  await unlink(config);
  await expect(present.update({ autoFetch: false })).rejects.toThrow("changed outside Gitbuddy");
  expect(await readdir(path)).toEqual([]);
});

test("symlink configs are refused both at load and when swapped in before save", async () => {
  const path = await directory();
  const target = join(path, "unrelated.json");
  const config = join(path, "config.json");
  const before = await SettingsStore.load(path);
  await writeFile(target, '{"autoFetch":false}');
  await symlink(target, config);
  const store = await SettingsStore.load(path);
  expect(store.value).toEqual(DEFAULT_SETTINGS);
  expect(store.warning).toContain("symlink");
  await expect(store.update({ theme: "catppuccin" })).rejects.toThrow("symlink");
  await expect(before.update({ theme: "catppuccin" })).rejects.toThrow("symlink");
  expect((await lstat(config)).isSymbolicLink()).toBe(true);
  expect(await readFile(target, "utf8")).toBe('{"autoFetch":false}');
});
