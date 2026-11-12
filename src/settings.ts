import { constants } from "node:fs";
import { lstat, mkdir, open, rename, unlink } from "node:fs/promises";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { randomUUID } from "node:crypto";

export interface Settings {
  autoFetch: boolean;
  fetchIntervalMinutes: number;
  /** Total commits retained per history view, across all pages. */
  maxCommits: number;
  theme: string;
  colorMode: "system" | "light" | "dark";
}

export const DEFAULT_SETTINGS: Settings = Object.freeze({
  autoFetch: true,
  fetchIntervalMinutes: 5,
  maxCommits: 100,
  theme: "opencode",
  colorMode: "system",
});

function validate(input: unknown): Settings {
  if (!input || typeof input !== "object" || Array.isArray(input)) {
    throw new Error("Settings must be a JSON object");
  }
  for (const key of Object.keys(input)) {
    if (!Object.hasOwn(DEFAULT_SETTINGS, key)) {
      throw new Error(`Unknown setting "${key}"; this file may require a newer Gitbuddy`);
    }
  }
  const value = { ...DEFAULT_SETTINGS, ...input };
  if (typeof value.autoFetch !== "boolean") throw new Error("autoFetch must be a boolean");
  if (!Number.isInteger(value.fetchIntervalMinutes) || value.fetchIntervalMinutes < 1 || value.fetchIntervalMinutes > 1440) {
    throw new Error("fetchIntervalMinutes must be an integer from 1 to 1440");
  }
  if (!Number.isSafeInteger(value.maxCommits) || value.maxCommits < 1) {
    throw new Error("maxCommits must be a positive safe integer");
  }
  if (typeof value.theme !== "string" || value.theme.length > 255 || !/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(value.theme) || value.theme.includes("..")) {
    throw new Error("theme must be a nonempty filename without paths or traversal");
  }
  if (!["system", "light", "dark"].includes(value.colorMode)) {
    throw new Error("colorMode must be system, light, or dark");
  }
  return value;
}

type Snapshot = { bytes: Buffer; ino: number; dev: number; mtimeMs: number; ctimeMs: number };

function missing(error: unknown): boolean {
  return (error as NodeJS.ErrnoException).code === "ENOENT";
}

/** Never follow a config symlink, including one swapped in after lstat. */
async function readConfig(path: string): Promise<Snapshot | null> {
  try {
    const entry = await lstat(path);
    if (!entry.isFile() || entry.isSymbolicLink()) throw new Error("config.json must be a regular file, not a symlink");
    const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
    try {
      const before = await file.stat();
      if (!before.isFile() || before.ino !== entry.ino || before.dev !== entry.dev) {
        throw new Error("config.json changed while being read");
      }
      const bytes = await file.readFile();
      const after = await file.stat();
      if (before.mtimeMs !== after.mtimeMs || before.ctimeMs !== after.ctimeMs) {
        throw new Error("config.json changed while being read");
      }
      return { bytes, ino: after.ino, dev: after.dev, mtimeMs: after.mtimeMs, ctimeMs: after.ctimeMs };
    } finally {
      await file.close();
    }
  } catch (error) {
    if (missing(error)) return null;
    throw error;
  }
}

function unchanged(first: Snapshot | null, second: Snapshot | null): boolean {
  if (!first || !second) return first === second;
  return first.ino === second.ino && first.dev === second.dev && first.mtimeMs === second.mtimeMs &&
    first.ctimeMs === second.ctimeMs && first.bytes.equals(second.bytes);
}

/** Owns only Gitbuddy's config.json. Unsafe/foreign files require repair and reload. */
export class SettingsStore {
  value: Settings = { ...DEFAULT_SETTINGS };
  warning: string | null = null;
  private original: Snapshot | null = null;
  private blocked: string | null = null;
  private pending: Promise<void> = Promise.resolve();

  private constructor(readonly directory: string) {}

  static async load(directory?: string): Promise<SettingsStore> {
    const store = new SettingsStore(resolve(directory ?? join(process.env.XDG_CONFIG_HOME || join(homedir(), ".config"), "gitbuddy")));
    try {
      store.original = await readConfig(store.path);
      if (store.original) store.value = validate(JSON.parse(store.original.bytes.toString("utf8")));
    } catch (error) {
      store.blocked = `Cannot safely use ${store.path}: ${String(error)}. Repair the file externally and reload Gitbuddy; saving is blocked.`;
      store.warning = store.blocked;
    }
    return store;
  }

  private get path(): string { return join(this.directory, "config.json"); }

  /** Valid changes take effect immediately, even if persistence rejects. Writes are ordered. */
  async update(patch: Partial<Settings>): Promise<void> {
    const next = validate({ ...this.value, ...patch });
    this.value = next;
    const operation = this.pending.then(() => this.save(next));
    this.pending = operation.catch(() => {});
    return operation;
  }

  private async save(value: Settings): Promise<void> {
    let temporary: string | undefined;
    try {
      if (this.blocked) throw new Error(this.blocked);
      await mkdir(this.directory, { recursive: true, mode: 0o700 });
      const candidate = join(this.directory, `.config.json.${randomUUID()}.tmp`);
      const file = await open(candidate, "wx", 0o600);
      temporary = candidate;
      let replacement: Snapshot;
      try {
        await file.chmod(0o600);
        const bytes = Buffer.from(JSON.stringify(value, null, 2) + "\n");
        await file.writeFile(bytes);
        await file.sync();
        const stat = await file.stat();
        replacement = { bytes, ino: stat.ino, dev: stat.dev, mtimeMs: stat.mtimeMs, ctimeMs: stat.ctimeMs };
      } finally {
        await file.close();
      }
      let current: Snapshot | null;
      try {
        current = await readConfig(this.path);
      } catch (error) {
        this.blocked = `Cannot safely replace ${this.path}: ${String(error)}. Repair externally and reload Gitbuddy.`;
        throw new Error(this.blocked);
      }
      if (!unchanged(this.original, current)) {
        this.blocked = `${this.path} changed outside Gitbuddy. Reload Gitbuddy before saving to preserve those edits.`;
        throw new Error(this.blocked);
      }
      // Compare immediately before the same-filesystem atomic replacement. No portable
      // filesystem CAS exists; another process must not edit during this rename window.
      await rename(temporary, this.path);
      temporary = undefined;
      // Rename changes ctime. Keep our own bytes/identity, not a later external file's.
      const stat = await lstat(this.path);
      this.original = { ...replacement, ctimeMs: stat.ctimeMs };
      this.warning = null;
    } catch (error) {
      this.warning = `Settings applied for this session but could not be saved to ${this.path}: ${String(error)}. Check the file and directory permissions, then retry (reload after external repairs).`;
      throw new Error(this.warning, { cause: error });
    } finally {
      if (temporary) await unlink(temporary).catch(() => {});
    }
  }
}
