import { spawn } from "node:child_process";
import { stat } from "node:fs/promises";

export type HistoryScope = "all" | "current";
export type PullMode = "ff-only" | "merge" | "rebase";

export interface RepositoryStatus {
  branch: string | null;
  head: string | null;
  upstream: string | null;
  dirty: boolean;
  operation: string | null;
  remotes: string[];
  fetchWarnings: string[];
}

/** Frozen commit tips and annotations; reuse this exact generation for every page. */
export interface HistorySnapshot {
  roots: string[];
  labels: Record<string, string[]>;
}

export interface CommitSummary {
  sha: string;
  subject: string;
  author: string;
  authoredAt: string;
  labels: string[];
}

export interface CommitDetails {
  sha: string;
  message: string;
  author: string;
  authorEmail: string;
  authoredAt: string;
  committer: string;
  committerEmail: string;
  committedAt: string;
  parents: string[];
  files: Array<{ status: string; path: string; oldPath?: string }>;
  comparison: string;
}

/** Presentation only: never apply this to filenames or other Git arguments. */
export function terminalSafe(text: string): string {
  return text.replace(/[\x00-\x09\x0b-\x1f\x7f-\x9f\u061c\u200b-\u200f\u2028-\u202e\u2060-\u206f\ufeff]/gu, (char) => {
    if (char === "\t") return "\\t";
    if (char === "\r") return "\\r";
    const code = char.codePointAt(0)!;
    return code <= 0xff
      ? `\\x${code.toString(16).padStart(2, "0")}`
      : `\\u${code.toString(16).padStart(4, "0")}`;
  });
}

interface GitResult { stdout: string; code: number }
interface Remote { name: string; skip: boolean; mappings: string[] }
const SHA = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/;
const FETCH_TIMEOUT_MS = 60_000;

function validateSha(sha: string): void {
  if (!SHA.test(sha)) throw new Error("Expected a full hexadecimal commit SHA.");
}

function environment(): NodeJS.ProcessEnv {
  const env = { ...process.env };
  // A launch-directory selection must not accidentally act on an ambient Git index/repo.
  for (const key of ["GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX"]) delete env[key];
  return {
    ...env,
    LC_ALL: "C",
    GIT_OPTIONAL_LOCKS: "0",
    GIT_TERMINAL_PROMPT: "0",
    GCM_INTERACTIVE: "Never",
    GIT_PAGER: "cat",
    GIT_EDITOR: "true",
    GIT_SEQUENCE_EDITOR: "true",
    GIT_MERGE_AUTOEDIT: "no",
  };
}

/** No shell, inherited terminal, or raw stderr (which can contain credential-bearing URLs). */
async function executeGit(cwd: string, args: string[], options: {
  input?: string;
  timeout?: number;
  detached?: boolean;
  env?: NodeJS.ProcessEnv;
} = {}): Promise<GitResult> {
  return new Promise((resolve, reject) => {
    const child = spawn("git", ["--no-pager", ...args], {
      cwd, env: options.env ?? environment(), stdio: ["pipe", "pipe", "pipe"],
      detached: options.detached ?? Boolean(options.timeout),
    });
    const chunks: Buffer[] = [];
    let timedOut = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let killTimer: ReturnType<typeof setTimeout> | undefined;
    const killGroup = (signal: NodeJS.Signals) => {
      if (child.pid) {
        try { process.kill(-child.pid, signal); }
        catch (error) { if ((error as NodeJS.ErrnoException).code !== "ESRCH") child.kill(signal); }
      }
    };
    if (options.timeout) timer = setTimeout(() => {
      timedOut = true;
      killGroup("SIGTERM");
      killTimer = setTimeout(() => killGroup("SIGKILL"), 1_000);
    }, options.timeout);
    child.stdout.on("data", (chunk: Buffer) => chunks.push(chunk));
    child.stderr.resume();
    child.stdin.on("error", () => { /* Early Git exit is reported by close, not EPIPE. */ });
    child.stdin.end(options.input);
    child.on("error", () => {
      clearTimeout(timer);
      clearTimeout(killTimer);
      reject(new Error("Cannot start Git. Check that Git is installed and the repository path is accessible."));
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      clearTimeout(killTimer);
      if (timedOut) reject(new Error("Fetch timed out after 60 seconds. Some refs may have updated; refresh and check connectivity/authentication before retrying."));
      else resolve({ stdout: Buffer.concat(chunks).toString("utf8"), code: code ?? -1 });
    });
  });
}

function checked(result: GitResult, action: string): string {
  if (result.code !== 0) throw new Error(`Git ${action} failed (exit ${result.code}). Inspect the repository with Git for diagnostics.`);
  return result.stdout;
}

function fields(output: string): string[] {
  if (!output) return [];
  if (!output.endsWith("\0")) throw new Error("Git returned an incomplete NUL-delimited record.");
  return output.slice(0, -1).split("\0");
}

function parseStatus(output: string): Pick<RepositoryStatus, "branch" | "head" | "upstream" | "dirty"> {
  let branch: string | null = null;
  let head: string | null = null;
  let upstream: string | null = null;
  let dirty = false;
  const records = fields(output);
  for (let i = 0; i < records.length; i++) {
    const record = records[i]!;
    if (record.startsWith("# branch.head ")) {
      const name = record.slice(14);
      branch = name === "(detached)" ? null : name;
    } else if (record.startsWith("# branch.oid ")) {
      const oid = record.slice(13);
      head = oid === "(initial)" ? null : oid;
    } else if (record.startsWith("# branch.upstream ")) upstream = record.slice(18);
    else if (!record.startsWith("# ")) {
      dirty = true;
      // Porcelain v2 renames have a second NUL-separated raw pathname.
      if (record.startsWith("2 ")) i++;
    }
  }
  return { branch, head, upstream, dirty };
}

/** Git owns locking; the session owns serialization and waits for active pulls on quit. */
export class GitRepository {
  private constructor(readonly root: string) {}

  /** Discovers a worktree (including unborn/linked worktrees), not a bare repository. */
  static async open(path: string): Promise<GitRepository> {
    const bare = await executeGit(path, ["rev-parse", "--is-bare-repository"]);
    if (bare.code !== 0) throw new Error("Not a Git repository. Open a directory inside a Git working tree.");
    if (bare.stdout.trim() === "true") throw new Error("Bare repositories have no working tree. Open a non-bare checkout instead.");
    const inside = checked(await executeGit(path, ["rev-parse", "--is-inside-work-tree"]), "discovery");
    if (inside.trim() !== "true") throw new Error("Open a directory inside the Git working tree, not its metadata directory.");
    // Strip only Git's final LF: the path itself can contain newlines and spaces.
    const root = checked(await executeGit(path, ["rev-parse", "--show-toplevel"]), "discovery").replace(/\n$/, "");
    return new GitRepository(root);
  }

  private async git(args: string[], input?: string): Promise<string> {
    return checked(await executeGit(this.root, args, { input }), args[0] ?? "command");
  }

  private async config(key: string): Promise<string[]> {
    const result = await executeGit(this.root, ["config", "--null", "--get-all", key]);
    if (result.code === 1) return []; // Documented config-key absence, not a general error fallback.
    return fields(checked(result, "config"));
  }

  private async remotes(): Promise<Remote[]> {
    const names = (await this.git(["remote"])).split("\n").filter(Boolean);
    return Promise.all(names.map(async (name) => {
      const [mappings, skipResult] = await Promise.all([
        this.config(`remote.${name}.fetch`),
        executeGit(this.root, ["config", "--bool", "--get", `remote.${name}.skipFetchAll`]),
      ]);
      if (skipResult.code !== 1) checked(skipResult, "remote configuration");
      return { name, mappings, skip: skipResult.code === 0 && skipResult.stdout.trim() === "true" };
    }));
  }

  private async operation(): Promise<string | null> {
    const markers = [
      ["rebase-merge", "rebase"], ["rebase-apply", "rebase"], ["MERGE_HEAD", "merge"],
      ["CHERRY_PICK_HEAD", "cherry-pick"], ["REVERT_HEAD", "revert"],
      ["sequencer", "sequencer"], ["BISECT_START", "bisect"], ["BISECT_LOG", "bisect"],
    ];
    const present = await Promise.all(markers.map(async ([marker, operation]) => {
      const path = (await this.git(["rev-parse", "--path-format=absolute", "--git-path", marker!])).replace(/\n$/, "");
      try { await stat(path); return operation!; }
      catch (error) { if ((error as NodeJS.ErrnoException).code === "ENOENT") return null; throw error; }
    }));
    return present.find(Boolean) ?? null;
  }

  async readStatus(): Promise<RepositoryStatus> {
    const [status, operation, remotes] = await Promise.all([
      this.git(["status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all"]),
      this.operation(), this.remotes(),
    ]);
    const fetchWarnings = remotes.flatMap(({ name, skip, mappings }) => {
      if (skip) return [`${name}: excluded from Fetch all by skipFetchAll.`];
      if (!mappings.some((mapping) => mapping.replace(/^\+/, "") === `refs/heads/*:refs/remotes/${name}/*`)
        || mappings.some((mapping) => mapping.startsWith("^"))) {
        return [`${name}: narrow or custom fetch mapping; Fetch all uses existing refspecs, not necessarily every remote branch.`];
      }
      return [];
    });
    return { ...parseStatus(status), operation, remotes: remotes.map(({ name }) => name), fetchWarnings };
  }

  async snapshot(scope: HistoryScope): Promise<HistorySnapshot> {
    if (scope !== "all" && scope !== "current") throw new Error("Unknown history scope.");
    const status = parseStatus(await this.git(["status", "--porcelain=v2", "--branch", "-z", "--untracked-files=no"]));
    const labels: Record<string, string[]> = {};
    const roots = new Set<string>();
    const add = (sha: string, label: string) => {
      validateSha(sha);
      if (scope === "all") roots.add(sha);
      const list = labels[sha] ??= [];
      if (!list.includes(label)) list.push(label);
    };
    // Annotations remain available in Current branch mode, but only HEAD is a root.
    const output = await this.git(["for-each-ref", "--format=%(objectname)%00%(refname)%00", "refs/heads/", "refs/remotes/", "refs/tags/"]);
    const refs = output ? output.replace(/\n$/, "").split("\n").map((line) => fields(line)) : [];
    if (refs.length) {
      // Batch peeling handles nested annotated tags and skips tags pointing to non-commits.
      // Revisions go through stdin, so even very many refs do not exhaust argv limits.
      const peeled = (await this.git(["cat-file", "--batch-check=%(objectname) %(objecttype)"], refs.map(([sha]) => `${sha}^{}\n`).join(""))).trimEnd().split("\n");
      if (peeled.length !== refs.length) throw new Error("Git returned an incomplete reference snapshot.");
      refs.forEach(([, ref], index) => {
        const line = peeled[index]!;
        const [sha, type] = line.split(" ");
        if (ref?.startsWith("refs/tags/") && (type === "tree" || type === "blob")) return;
        if (type !== "commit" || !sha || !ref) throw new Error("Git returned an invalid commit reference.");
        const label = ref.startsWith("refs/tags/") ? `tag: ${ref.slice(10)}` : ref.replace(/^refs\/(heads|remotes)\//, "");
        add(sha, label);
      });
    }
    if (status.head) {
      roots.add(status.head);
      add(status.head, "HEAD");
    }
    return { roots: [...roots], labels };
  }

  /** Default page size 200; order and labels are derived only from the supplied frozen snapshot. */
  async readHistoryPage(snapshot: HistorySnapshot, offset: number, limit = 200): Promise<CommitSummary[]> {
    if (!Number.isSafeInteger(offset) || offset < 0 || !Number.isSafeInteger(limit) || limit < 1) throw new Error("History offset must be nonnegative and page size must be positive integers.");
    snapshot.roots.forEach(validateSha);
    if (!snapshot.roots.length) return [];
    const records = fields(await this.git([
      "log", "--topo-order", "--no-notes", "--no-show-signature", "-z", "--format=%H%x00%s%x00%an%x00%aI",
      `--skip=${offset}`, `--max-count=${limit}`, "--stdin", "--",
    ], snapshot.roots.join("\n") + "\n"));
    if (records.length % 4) throw new Error("Git returned malformed history records.");
    const commits: CommitSummary[] = [];
    for (let i = 0; i < records.length; i += 4) {
      const [sha, subject, author, authoredAt] = records.slice(i, i + 4) as [string, string, string, string];
      validateSha(sha);
      commits.push({ sha, subject, author, authoredAt, labels: [...(snapshot.labels[sha] ?? [])] });
    }
    return commits;
  }

  /** Merge file lists compare with the first parent; no patch/external diff is executed. */
  async readCommitDetails(sha: string): Promise<CommitDetails> {
    validateSha(sha);
    const metadata = fields(await this.git([
      "log", "-1", "-z", "--no-notes", "--no-show-signature",
      "--format=%H%x00%B%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%P", sha, "--",
    ]));
    if (metadata.length !== 9) throw new Error("Git returned malformed commit details.");
    const [resolved, message, author, authorEmail, authoredAt, committer, committerEmail, committedAt, parentText] = metadata as [string, string, string, string, string, string, string, string, string];
    const parents = parentText ? parentText.split(" ") : [];
    const diffArgs = ["--no-ext-diff", "--no-textconv", "--name-status", "-z", "-M"];
    const names = fields(await this.git(parents.length
      ? ["diff", ...diffArgs, parents[0]!, sha, "--"]
      : ["diff-tree", "--root", "--no-commit-id", "-r", ...diffArgs, sha, "--"]));
    const files: CommitDetails["files"] = [];
    for (let i = 0; i < names.length;) {
      const status = names[i++]!;
      if (!/^[ACDMRTUXB][0-9]*$/.test(status)) throw new Error("Git returned an invalid changed-file status.");
      const firstPath = names[i++];
      if (firstPath === undefined) throw new Error("Git returned an incomplete changed-file record.");
      if (/^[RC]/.test(status)) {
        const path = names[i++];
        if (path === undefined) throw new Error("Git returned an incomplete rename record.");
        files.push({ status, oldPath: firstPath, path });
      } else files.push({ status, path: firstPath });
    }
    return {
      sha: resolved, message, author, authorEmail, authoredAt, committer, committerEmail, committedAt, parents, files,
      comparison: parents.length > 1 ? `Compared with first parent ${parents[0]}` : parents.length ? `Compared with parent ${parents[0]}` : "Root commit: all files added",
    };
  }

  private async mutationEnvironment(): Promise<NodeJS.ProcessEnv> {
    const env = environment();
    const configured = (await this.config("core.sshCommand")).at(-1);
    const ssh = env.GIT_SSH_COMMAND || configured || (env.GIT_SSH ? `'${env.GIT_SSH.replaceAll("'", "'\\''")}'` : "ssh");
    // Preserve the user's SSH executable, trust policy and configuration; never accept host keys.
    env.GIT_SSH_COMMAND = `${ssh} -o BatchMode=yes`;
    env.GIT_ASKPASS = "false";
    env.SSH_ASKPASS = "false";
    env.SSH_ASKPASS_REQUIRE = "never";
    return env;
  }

  private validateFetchMappings(remotes: Remote[]): void {
    for (const remote of remotes) {
      if (remote.mappings.some((mapping) => {
        const destination = mapping.split(":")[1];
        // Unqualified destinations can resolve to local branches, too.
        if (!destination) return false;
        if (!destination.startsWith("refs/")) return true;
        const [prefix, suffix, ...extra] = destination.split("*");
        if (suffix === undefined) return destination === "refs/heads" || destination.startsWith("refs/heads/");
        // A wildcard can consume the rest of the heads namespace and any branch
        // path before its suffix. Only disjoint literal prefixes are safe.
        return extra.length > 0 || "refs/heads/".startsWith(prefix!) || prefix!.startsWith("refs/heads/");
      })) throw new Error(`Refusing fetch: ${terminalSafe(remote.name)} has a fetch mapping targeting local branches. Review its refspecs outside Gitbuddy.`);
    }
  }

  private noPruneArgs(remotes: Remote[]): string[] {
    // --no-prune alone does not neutralize ambient pruneTags (which adds a tag refspec).
    return ["-c", "fetch.all=false", "-c", "fetch.prune=false", "-c", "fetch.pruneTags=false", ...remotes.flatMap(({ name }) => [
      "-c", `remote.${name}.prune=false`, "-c", `remote.${name}.pruneTags=false`,
    ])];
  }

  /** Fetches existing eligible mappings, never prunes, and bounds noninteractive network work. */
  async fetchAll(): Promise<void> {
    const remotes = await this.remotes();
    const eligible = remotes.filter(({ skip }) => !skip);
    if (!eligible.length) throw new Error("No eligible remotes. Configure a remote (or review skipFetchAll) before fetching.");
    this.validateFetchMappings(eligible);
    const config = this.noPruneArgs(remotes);
    const result = await executeGit(this.root, [...config, "fetch", "--all", "--no-prune"], {
      env: await this.mutationEnvironment(), timeout: FETCH_TIMEOUT_MS,
    });
    if (result.code !== 0) throw new Error(`Fetch failed (exit ${result.code}). Some refs may have updated; refresh and check remote connectivity/authentication with Git. No pruning was requested.`);
  }

  /** Rechecks preconditions immediately before integration; failures/conflicts are never rolled back. */
  async pull(mode: PullMode): Promise<void> {
    if (!["ff-only", "merge", "rebase"].includes(mode)) throw new Error("Unknown pull mode.");
    const status = await this.readStatus();
    if (status.operation) throw new Error(`Cannot pull during ${status.operation}. Resolve or finish that operation outside Gitbuddy.`);
    if (!status.branch || !status.head) throw new Error("Cannot pull without an attached, committed branch. Select a branch outside Gitbuddy.");
    if (!status.upstream) throw new Error("Cannot pull without an upstream. Configure the branch upstream outside Gitbuddy.");
    if (status.dirty) throw new Error("Cannot pull with index, working-tree or untracked changes. Commit, stash or remove them yourself outside Gitbuddy.");
    const remotes = await this.remotes();
    const upstreamRemote = (await this.config(`branch.${status.branch}.remote`)).at(-1);
    // skipFetchAll does not exclude an explicitly requested pull. A local (.) upstream is valid.
    this.validateFetchMappings(remotes.filter(({ name }) => name === upstreamRemote));
    const config = this.noPruneArgs(remotes);
    const args = [
      ...config, "-c", "pull.rebase=false", "-c", "pull.ff=true",
      "-c", "rebase.autoStash=false", "-c", "merge.autoStash=false", "-c", "rebase.updateRefs=false",
      "pull", "--no-prune", "--no-autostash",
      ...(mode === "rebase" ? ["--rebase", "--ff"] : mode === "merge" ? ["--no-rebase", "--ff"] : ["--no-rebase", "--ff-only"]),
    ];
    // Deliberately no timeout/cancellation: quitting must wait, not interrupt an integration.
    // A separate process session also denies /dev/tty auth prompts from custom SSH commands.
    const result = await executeGit(this.root, args, { env: await this.mutationEnvironment(), detached: true });
    if (result.code !== 0) {
      const operation = await this.operation();
      throw new Error(`Pull ${mode} failed (exit ${result.code}). ${operation ? `${operation} state is preserved; resolve or abort it yourself outside Gitbuddy.` : "No automatic reset, stash or abort was performed. Check divergence, upstream and connectivity with Git."}`);
    }
  }
}
