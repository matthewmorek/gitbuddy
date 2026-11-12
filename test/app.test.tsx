import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { testRender } from "@opentui/solid";
import type { CapturedFrame } from "@opentui/core";
import { ManualClock } from "@opentui/core/testing";
import { App } from "../src/app";
import { RepositorySession, type Repository } from "../src/repository-session";
import { SettingsStore } from "../src/settings";
import { ThemeCatalog } from "../src/theme";
import { temporaryRoot } from "./temp";

const cleanups: (() => Promise<void>)[] = [];
afterEach(async () => { for (const cleanup of cleanups.splice(0).reverse()) await cleanup(); });

async function setup(width: number, clock?: ManualClock) {
  const directory = await mkdtemp(join(temporaryRoot, "gitbuddy-ui-"));
  cleanups.push(() => rm(directory, { recursive: true, force: true }));
  const settings = await SettingsStore.load(directory);
  const themes = await ThemeCatalog.load(directory);
  const rows = ["a", "b", "c"].map(letter => ({ sha: letter.repeat(40), subject: `Commit ${letter}`, author: "Author", authoredAt: "2026-11-12", labels: letter === "a" ? ["main"] : [] }));
  const repository: Repository = {
    root: "/example/project", readStatus: async () => ({ branch: "main", head: rows[0]!.sha, upstream: null, dirty: false, operation: null, remotes: [], fetchWarnings: [] }),
    snapshot: async () => ({ roots: [rows[0]!.sha], labels: {} }),
    readHistoryPage: async () => rows,
    readCommitDetails: async sha => ({ sha, message: `Full message for ${sha.slice(0, 1)}`, author: "Author", authorEmail: "a@example.test", authoredAt: "2026-11-12", committer: "Author", committerEmail: "a@example.test", committedAt: "2026-11-12", parents: [], files: [{ status: "A", path: "file.ts" }], comparison: "Root commit" }),
    fetchAll: async () => {}, pull: async () => {},
  };
  const session = new RepositorySession(repository);
  await session.refresh();
  await session.select(rows[0]!.sha);
  let quitCount = 0;
  const ui = await testRender(() => <App session={session} settings={settings} themes={themes} quit={async () => { quitCount++; }} />, { width, height: 28, kittyKeyboard: true, clock });
  cleanups.push(async () => { await session.close(); await ui.dispose(); });
  await ui.renderOnce();
  return { ...ui, session, repository, settings, themes, rows, directory, quitCount: () => quitCount };
}

function cell(frame: CapturedFrame, x: number, y: number) {
  let column = 0;
  for (const span of frame.lines[y]!.spans) {
    if (x < column + span.width) return span;
    column += span.width;
  }
  throw new Error(`Missing rendered cell ${x},${y}`);
}
const brightness = (color: { r: number; g: number; b: number }) => color.r * 0.2126 + color.g * 0.7152 + color.b * 0.0722;

test("palette captures typing/navigation and restores history focus and selection", async () => {
  const ui = await setup(120);
  expect(ui.captureCharFrame()).toContain("Commit a");
  ui.mockInput.pressKey("p", { ctrl: true });
  await ui.renderOnce();
  await ui.mockInput.typeText("jqk");
  ui.mockInput.pressArrow("down");
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("No matching commands");
  expect(ui.session.state.selected).toBe(ui.rows[0]!.sha);
  expect(ui.quitCount()).toBe(0);
  ui.mockInput.pressEscape();
  await ui.renderOnce();
  ui.mockInput.pressArrow("down");
  await ui.renderOnce();
  expect(ui.session.state.selected).toBe(ui.rows[1]!.sha);
});

test("Ctrl+P searches individual settings, applies a theme, and persists it", async () => {
  const ui = await setup(120);
  ui.mockInput.pressKey("p", { ctrl: true });
  await ui.renderOnce();
  await ui.mockInput.typeText("settings theme");
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toMatch(/Theme\s+.*Settings › Appearance/);
  ui.mockInput.pressEnter();
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Settings · theme");
  await ui.mockInput.typeText("catppuccin");
  await ui.renderOnce();
  ui.mockInput.pressEnter();
  await ui.waitFor(async () => {
    const config = Bun.file(join(ui.directory, "config.json"));
    return await config.exists() && (await config.json()).theme === "catppuccin";
  });
  expect(ui.settings.value.theme).toBe("catppuccin");
  expect(ui.session.state.selected).toBe(ui.rows[0]!.sha);
});

test("narrow layout supports details, modal focus restoration and Escape back", async () => {
  const ui = await setup(75);
  expect(ui.captureCharFrame()).not.toContain("Full message for a");
  ui.mockInput.pressEnter();
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Full message for a");
  ui.mockInput.pressKey("p", { ctrl: true });
  await ui.renderOnce();
  ui.mockInput.pressTab();
  ui.mockInput.pressEscape();
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Full message for a");
  ui.mockInput.pressEscape();
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Commit a");
  expect(ui.captureCharFrame()).not.toContain("Full message for a");
});

test("details keep their reading position through status, refresh and fetch updates, but reset for a new SHA", async () => {
  const ui = await setup(120);
  const readDetails = ui.repository.readCommitDetails;
  const readStatus = ui.repository.readStatus;
  ui.repository.readStatus = async () => ({ ...await readStatus(), remotes: ["origin"] });
  ui.repository.readCommitDetails = async sha => ({ ...await readDetails(sha), message: Array.from({ length: 60 }, (_, index) => `Reading line ${String(index).padStart(2, "0")}`).join("\n") });
  await ui.session.refresh();
  await ui.session.select(ui.rows[0]!.sha);
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Reading line 00");
  ui.mockInput.pressTab();
  for (let index = 0; index < 12; index++) ui.mockInput.pressArrow("down");
  await ui.renderOnce();
  const readingLines = () => ui.captureCharFrame().match(/Reading line \d+/g);
  const scrolled = readingLines();
  expect(scrolled?.length).toBeGreaterThan(0);
  expect(scrolled).not.toContain("Reading line 00");
  ui.session.reportError("status update");
  await ui.renderOnce();
  // A warning adds a footer row, but must not move the first visible line.
  expect(readingLines()?.[0]).toBe(scrolled?.[0]);
  await ui.session.refresh();
  await ui.renderOnce();
  expect(readingLines()?.[0]).toBe(scrolled?.[0]);
  await ui.session.fetch();
  await ui.renderOnce();
  expect(readingLines()?.[0]).toBe(scrolled?.[0]);
  await ui.session.select(ui.rows[1]!.sha);
  await ui.renderOnce();
  expect(readingLines()).toContain("Reading line 00");
});

test("a short Current view renders after both deep selection and viewport anchor leave All history", async () => {
  const ui = await setup(120);
  let rows = Array.from({ length: 60 }, (_, index) => ({ ...ui.rows[0]!, sha: index.toString(16).padStart(40, "0"), subject: `Deep commit ${index}` }));
  ui.repository.readHistoryPage = async () => rows;
  await ui.session.refresh();
  const selected = rows[49]!.sha;
  await ui.session.select(selected);
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Deep commit 49");
  rows = ui.rows.slice(0, 1);
  await ui.session.refresh("current");
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Commit a");
  expect(ui.captureCharFrame()).toContain("outside the loaded history");
  expect(ui.session.state.selected).toBe(selected);
  expect(ui.session.state.details?.sha).toBe(selected);
  ui.resize(120, 36);
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Commit a");
  ui.mockInput.pressArrow("down");
  await ui.renderOnce();
  expect(ui.session.state.selected).toBe(ui.rows[0]!.sha);
  expect(ui.captureCharFrame()).toContain("▌ Commit a");
});

test.each(["dark", "light"] as const)("borderless %s panels retain outer padding and move the darker surface with focus", async mode => {
  const ui = await setup(120);
  ui.mockInput.pressKey("p", { ctrl: true });
  await ui.renderOnce();
  await ui.mockInput.typeText("color mode");
  ui.mockInput.pressEnter();
  await ui.renderOnce();
  await ui.mockInput.typeText(mode);
  ui.mockInput.pressEnter();
  await ui.waitFor(() => ui.settings.value.colorMode === mode);
  await ui.renderOnce();
  const lines = ui.captureCharFrame().split("\n").slice(0, 28);
  expect(lines[0]!.trim()).toBe("");
  expect(lines[27]!.trim()).toBe("");
  expect(lines.every(line => line.startsWith(" ") && line.endsWith(" "))).toBe(true);
  expect(lines.join("\n")).not.toMatch(/[┌┐└┘─│╭╮╰╯▶]/);
  const y = lines.findIndex(line => line.includes("Commit details"));
  const left = lines[y]!.indexOf("History");
  const right = lines[y]!.indexOf("Commit details");
  const before = ui.captureSpans();
  const theme = ui.themes.resolve(ui.settings.value.theme, mode);
  expect(brightness(cell(before, left, y).bg)).toBeLessThan(brightness(cell(before, right, y).bg));
  // Panel padding is real blank space, not a border masquerading as spacing.
  expect(lines[y - 1]!.trim()).toBe("");
  expect(lines[y]![left - 1]).toBe(" ");
  const commitLine = lines.findIndex(line => line.includes("Commit a"));
  expect(commitLine).toBe(y + 2); // One blank row separates the heading from history.
  const edge = lines[commitLine]!.indexOf("▌");
  expect(lines[commitLine + 1]![edge]).toBe("▌");
  expect(cell(before, edge, commitLine).fg.equals(theme.hue.accent[200])).toBe(true);
  expect(cell(before, edge, commitLine).fg.equals(cell(before, edge, commitLine + 1).fg)).toBe(true);
  expect(cell(before, edge + 2, commitLine).bg.equals(cell(before, right - 3, commitLine).bg)).toBe(true);
  const header = lines.findIndex(line => line.includes("gitbuddy ·"));
  const status = lines.findIndex(line => line.includes("No remotes"));
  const hotkeys = lines.findIndex(line => line.includes("↑↓/jk Navigate"));
  expect(lines[header]!.indexOf("gitbuddy")).toBe(2);
  expect(lines[status]!.indexOf("No remotes")).toBe(2);
  expect(lines[hotkeys]!.indexOf("↑↓/jk")).toBe(2);
  expect(lines[hotkeys]!.slice(-2)).toBe("  ");
  expect(cell(before, 1, hotkeys).bg.equals(theme.background.raised.base)).toBe(true);
  expect(cell(before, 118, hotkeys).bg.equals(theme.background.raised.base)).toBe(true);
  expect(lines.join("\n")).not.toMatch(/commits loaded|limit 100|auto-fetch/i);
  ui.mockInput.pressTab();
  await ui.renderOnce();
  const after = ui.captureSpans();
  expect(brightness(cell(after, right, y).bg)).toBeLessThan(brightness(cell(after, left, y).bg));
  // The modal dims background text/surfaces, not its opaque contents. Escape
  // removes the tint and preserves the previously focused details pane.
  ui.mockInput.pressKey("p", { ctrl: true });
  await ui.renderOnce();
  const overlay = ui.captureSpans();
  expect(brightness(cell(overlay, 2, header).fg)).toBeLessThan(brightness(cell(after, 2, header).fg));
  expect(cell(overlay, 2, header).bg.equals(cell(after, 2, header).bg)).toBe(false);
  const modalLines = ui.captureCharFrame().split("\n");
  const optionY = modalLines.findIndex(line => line.includes("Fetch now"));
  const optionX = modalLines[optionY]!.indexOf("Fetch now");
  const optionEdge = modalLines[optionY]!.indexOf("▌", optionX - 2);
  expect(cell(overlay, optionEdge, optionY).fg.equals(theme.hue.accent[200])).toBe(true);
  expect(cell(overlay, optionX, optionY).bg.equals(theme.background.raised.high)).toBe(true);
  ui.mockInput.pressEscape();
  await ui.renderOnce();
  expect(cell(ui.captureSpans(), 2, header).fg.equals(cell(after, 2, header).fg)).toBe(true);
  expect(brightness(cell(ui.captureSpans(), right, y).bg)).toBeLessThan(brightness(cell(ui.captureSpans(), left, y).bg));
});

test("palette uses padded single-line options, a full-row highlight and muted right-aligned categories", async () => {
  const ui = await setup(120);
  ui.mockInput.pressKey("p", { ctrl: true });
  await ui.renderOnce();
  const lines = ui.captureCharFrame().split("\n");
  const y = lines.findIndex(line => line.includes("Fetch now"));
  expect(lines[y + 1]).toContain("Pull — fast-forward only");
  expect(lines[y + 2]).toContain("Pull — merge");
  const categoryEnd = lines[y]!.lastIndexOf("Git") + 3;
  expect(lines[y + 1]!.lastIndexOf("Git") + 3).toBe(categoryEnd);
  const frame = ui.captureSpans();
  const edge = lines[y]!.indexOf("▌");
  const name = lines[y]!.indexOf("Fetch now");
  const category = lines[y]!.lastIndexOf("Git");
  expect(edge).toBeGreaterThan(0);
  expect(cell(frame, name, y).bg.equals(cell(frame, categoryEnd, y).bg)).toBe(true);
  expect(cell(frame, edge, y).fg.equals(cell(frame, category, y).fg)).toBe(false);
  expect(lines.join("\n")).not.toMatch(/[┌┐└┘─│╭╮╰╯]/);
  expect(ui.captureCharFrame()).toContain("No remotes configured"); // Unavailable reason remains inspectable.
  await ui.mockInput.typeText("max commits");
  await ui.renderOnce();
  const filtered = ui.captureCharFrame().split("\n").find(line => line.includes("Maximum commits"))!;
  expect(filtered).toMatch(/Maximum commits\s+100 · Settings › History/);
});

test("maximum commits is searchable, changes loaded history immediately and persists", async () => {
  const ui = await setup(75);
  const rows = Array.from({ length: 120 }, (_, index) => ({ ...ui.rows[0]!, sha: index.toString(16).padStart(40, "0"), subject: `Capped commit ${index}` }));
  ui.repository.readHistoryPage = async (_snapshot, offset, limit) => rows.slice(offset, offset + limit!);
  await ui.session.refresh();
  expect(ui.session.state.rows).toHaveLength(100);
  await ui.session.select(rows[80]!.sha);
  ui.mockInput.pressKey("p", { ctrl: true });
  await ui.renderOnce();
  await ui.mockInput.typeText("max commits");
  ui.mockInput.pressEnter();
  await ui.renderOnce();
  ui.mockInput.pressKey("a", { ctrl: true });
  ui.mockInput.pressKey("k", { ctrl: true });
  await ui.mockInput.typeText("25");
  ui.mockInput.pressEnter();
  await ui.waitFor(async () => {
    const config = Bun.file(join(ui.directory, "config.json"));
    return await config.exists() && (await config.json()).maxCommits === 25;
  });
  expect(ui.session.state.rows).toHaveLength(25);
  expect(ui.session.state.hasMore).toBe(false);
  expect(ui.session.state.selected).toBe(rows[80]!.sha);
  await ui.renderOnce();
  expect(ui.captureCharFrame()).not.toMatch(/commits loaded|limit 25/);
});

test("a compact palette keeps its final command reachable without losing terminal padding", async () => {
  const ui = await setup(60);
  ui.resize(60, 18);
  ui.mockInput.pressKey("p", { ctrl: true });
  for (let index = 0; index < 25; index++) ui.mockInput.pressArrow("down");
  await ui.renderOnce();
  const frame = ui.captureCharFrame();
  expect(frame).toMatch(/▌ Quit\s+q · App/);
  expect(frame).toContain("↑↓ Choose · Enter Apply · Esc Back");
  const lines = frame.split("\n").slice(0, 18);
  expect(lines[0]!.trim()).toBe("");
  expect(lines[17]!.trim()).toBe("");
  ui.mockInput.pressEnter();
  await ui.renderOnce();
  expect(ui.quitCount()).toBe(1);
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

test("history navigation keeps remote status quiet and shows only slow local detail loading in its pane", async () => {
  const clock = new ManualClock();
  const ui = await setup(120, clock);
  const readStatus = ui.repository.readStatus;
  ui.repository.readStatus = async () => ({ ...await readStatus(), remotes: ["origin"] });
  await ui.session.refresh();
  const readDetails = ui.repository.readCommitDetails;
  const first = deferred<Awaited<ReturnType<typeof readDetails>>>();
  const second = deferred<Awaited<ReturnType<typeof readDetails>>>();
  const reads: string[] = [];
  let fetches = 0;
  ui.repository.fetchAll = async () => { fetches++; };
  ui.repository.readCommitDetails = sha => {
    reads.push(sha);
    return sha === ui.rows[1]!.sha ? first.promise : second.promise;
  };
  ui.mockInput.pressArrow("down");
  await ui.renderOnce();
  expect(ui.session.state.selected).toBe(ui.rows[1]!.sha);
  expect(ui.session.state.detailsLoading).toBe(true);
  expect(ui.captureCharFrame()).toContain("Not fetched this session");
  expect(ui.captureCharFrame()).not.toContain("Loading commit");
  expect(fetches).toBe(0);
  expect(ui.session.state.operation).toBe(null);

  // Moving again starts a new delay, not the previous selection's deadline.
  clock.advance(100);
  ui.mockInput.pressArrow("down");
  await ui.renderOnce();
  clock.advance(100);
  await ui.renderOnce();
  expect(ui.captureCharFrame()).not.toContain("Loading commit");
  first.resolve(await readDetails(ui.rows[1]!.sha));
  await ui.renderOnce();
  expect(ui.session.state.detailsLoading).toBe(true);
  clock.advance(50);
  await ui.renderOnce();
  const lines = ui.captureCharFrame().split("\n");
  const heading = lines.findIndex(line => line.includes("Commit details"));
  const loading = lines.findIndex(line => /[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] Loading commit/.test(line));
  const footer = lines.findIndex(line => line.includes("Not fetched this session"));
  expect(loading).toBeGreaterThan(heading);
  expect(loading).toBeLessThan(footer);
  expect(lines[loading]!.indexOf("Loading commit")).toBeGreaterThan(60);
  expect(reads).toEqual([ui.rows[1]!.sha, ui.rows[2]!.sha]);
  expect(fetches).toBe(0);

  second.resolve(await readDetails(ui.rows[2]!.sha));
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Full message for c");
  expect(ui.captureCharFrame()).not.toContain("Loading commit");
  ui.mockInput.pressArrow("up"); // Already-resolved local read must not flash later.
  await ui.renderOnce();
  clock.advance(200);
  await ui.renderOnce();
  expect(ui.captureCharFrame()).toContain("Full message for b");
  expect(ui.captureCharFrame()).not.toMatch(/[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]/);
  expect(ui.captureCharFrame()).toContain("Not fetched this session");
  expect(fetches).toBe(0);
});

test("the activity line labels history, paging, fetch and pull until each operation settles", async () => {
  const ui = await setup(120);
  const showActivity = async (label: string) => {
    await ui.renderOnce();
    expect(ui.captureCharFrame()).toMatch(new RegExp(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] ${label}`));
  };
  const noActivity = async () => {
    await ui.renderOnce();
    expect(ui.captureCharFrame()).not.toMatch(/[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]/);
  };
  const history = deferred<typeof ui.rows>();
  ui.repository.readHistoryPage = () => history.promise;
  const refresh = ui.session.refresh();
  await showActivity("Loading history");
  history.resolve(ui.rows);
  await refresh;
  await noActivity();

  const many = Array.from({ length: 450 }, (_, index) => ({ ...ui.rows[0]!, sha: index.toString(16).padStart(40, "0") }));
  ui.repository.readHistoryPage = async (_snapshot, offset, limit) => many.slice(offset, offset + limit!);
  await ui.session.setMaxCommits(500);
  const page = deferred<typeof ui.rows>();
  ui.repository.readHistoryPage = () => page.promise;
  const paging = ui.session.loadMore();
  await showActivity("Loading more commits");
  page.resolve(many.slice(200));
  await paging;
  await noActivity();

  const readStatus = ui.repository.readStatus;
  ui.repository.readStatus = async () => ({ ...await readStatus(), remotes: ["origin"], upstream: "origin/main" });
  ui.repository.readHistoryPage = async () => ui.rows;
  await ui.session.refresh();
  for (const operation of ["fetch", "pull"] as const) {
    const pending = deferred<void>();
    ui.repository.fetchAll = () => pending.promise;
    ui.repository.pull = () => pending.promise;
    const task = operation === "fetch" ? ui.session.fetch() : ui.session.pull("ff-only");
    await showActivity(operation === "fetch" ? "Fetching" : "Pulling");
    pending.resolve();
    await task;
    await noActivity();
  }
});
