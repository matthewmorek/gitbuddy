import { afterEach, expect, test } from "bun:test";
import { mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { GitRepository, terminalSafe } from "../src/git";
import { fixture } from "./git-fixture";

const owned: Awaited<ReturnType<typeof fixture>>[] = [];
async function setup() { const f = await fixture(); owned.push(f); return f; }
afterEach(async () => { await Promise.all(owned.splice(0).map((f) => f.cleanup())); });

test("discovers subdirectories, unborn branches, and rejects bare/non-repositories", async () => {
  const f = await setup();
  const path = await f.repo();
  await mkdir(join(path, "sub dir"));
  const repo = await GitRepository.open(join(path, "sub dir"));
  expect(repo.root).toBe(path);
  expect(await repo.readStatus()).toEqual({ branch: "main", head: null, upstream: null, dirty: false, operation: null, remotes: [], fetchWarnings: [] });
  expect(await repo.readHistoryPage(await repo.snapshot("all"), 0)).toEqual([]);
  await expect(repo.fetchAll()).rejects.toThrow("No eligible remotes");
  await expect(GitRepository.open(f.root)).rejects.toThrow("Not a Git repository");
  await expect(GitRepository.open(await f.repo("bare.git", true))).rejects.toThrow("Bare repositories");
});

test("frozen roots/labels paginate topologically, peel tags and exclude stash/reflogs", async () => {
  const f = await setup();
  const path = await f.repo();
  const first = await f.commit(path, "first");
  await f.git(path, "tag", "light");
  await f.git(path, "tag", "-a", "annotated", "-m", "tag message");
  await f.git(path, "tag", "-a", "nested", "annotated", "-m", "nested tag");
  await f.git(path, "tag", "blob-tag", await f.git(path, "rev-parse", "HEAD:first"));
  const second = await f.commit(path, "second");
  await f.git(path, "branch", "side", first);
  await f.git(path, "update-ref", "refs/remotes/origin/main", second);
  await writeFile(join(path, "stash-only"), "stash");
  await f.git(path, "stash", "push", "--include-untracked", "-m", "not history");
  const stash = await f.git(path, "rev-parse", "refs/stash");
  const repo = await GitRepository.open(path);
  const snapshot = await repo.snapshot("all");
  expect(snapshot.labels[first]).toEqual(["side", "tag: annotated", "tag: light", "tag: nested"]);
  expect(snapshot.labels[second]).toEqual(["main", "origin/main", "HEAD"]);
  expect(snapshot.roots).not.toContain(stash);
  const third = await f.commit(path, "third");
  const page1 = await repo.readHistoryPage(snapshot, 0, 1);
  const page2 = await repo.readHistoryPage(snapshot, 1, 1);
  expect([...page1, ...page2].map((commit) => commit.sha)).toEqual([second, first]);
  expect(await repo.readHistoryPage(snapshot, 2)).toEqual([]);
  const current = await repo.snapshot("current");
  expect(current.roots).toEqual([third]);
  expect(current.labels[first]).toEqual(snapshot.labels[first]);
  expect(current.labels[third]).toEqual(["main", "HEAD"]);
  await expect(repo.readCommitDetails("--all")).rejects.toThrow("SHA");
  await expect(repo.readHistoryPage({ roots: ["HEAD"], labels: {} }, 0)).rejects.toThrow("SHA");
});

test("details preserve full metadata, root additions, rename and newline filenames; merges name first parent", async () => {
  const f = await setup();
  const path = await f.repo();
  const original = "odd\n\t\u001b[31m.txt";
  const renamed = "renamed\nfile.txt";
  const root = await f.commit(path, original, "identical content\n", "subject\n\nfull body\nlast line");
  const repo = await GitRepository.open(path);
  const details = await repo.readCommitDetails(root);
  expect(details.message).toBe("subject\n\nfull body\nlast line\n");
  expect(details.author).toBe("Fixture Author");
  expect(details.authorEmail).toBe("author@example.invalid");
  expect(details.committer).toBe("Fixture Committer");
  expect(details.committerEmail).toBe("committer@example.invalid");
  expect(details.authoredAt).toMatch(/^\d{4}-/);
  expect(details.parents).toEqual([]);
  expect(details.files).toEqual([{ status: "A", path: original }]);
  expect(details.comparison).toContain("Root commit");
  await rename(join(path, original), join(path, renamed));
  await f.git(path, "add", "--all");
  await f.git(path, "commit", "-m", "rename");
  const renameSha = await f.git(path, "rev-parse", "HEAD");
  expect((await repo.readCommitDetails(renameSha)).files).toEqual([{ status: "R100", oldPath: original, path: renamed }]);
  await f.git(path, "switch", "-c", "side", root);
  const side = await f.commit(path, "side-file");
  await f.git(path, "switch", "main");
  await f.git(path, "merge", "--no-ff", "side", "-m", "merge");
  const merge = await repo.readCommitDetails(await f.git(path, "rev-parse", "HEAD"));
  expect(merge.parents).toEqual([renameSha, side]);
  expect(merge.files).toEqual([{ status: "A", path: "side-file" }]);
  expect(merge.comparison).toContain(`first parent ${renameSha}`);
  const ordered = (await repo.readHistoryPage(await repo.snapshot("all"), 0)).map((commit) => commit.sha);
  expect(ordered.indexOf(merge.sha)).toBeLessThan(ordered.indexOf(renameSha));
  expect(ordered.indexOf(side)).toBeLessThan(ordered.indexOf(root));
});

test("fetch updates existing mappings without pruning refs/tags or ignoring skipFetchAll", async () => {
  const f = await setup();
  const { local, writer, remote } = await f.paired();
  const repo = await GitRepository.open(local);
  const before = (await repo.readStatus()).head;
  await f.git(local, "tag", "local-only");
  await f.git(local, "update-ref", "refs/remotes/origin/deleted", before!);
  for (const key of ["fetch.prune", "fetch.pruneTags", "remote.origin.prune", "remote.origin.pruneTags"]) await f.git(local, "config", key, "true");
  await f.git(local, "remote", "add", "skipped", join(f.root, "does-not-exist"));
  await f.git(local, "config", "remote.skipped.skipFetchAll", "true");
  const next = await f.commit(writer, "remote-next");
  await f.git(writer, "push", "origin", "main");
  await repo.fetchAll();
  expect(await f.git(local, "rev-parse", "refs/remotes/origin/main")).toBe(next);
  expect(await f.git(local, "rev-parse", "refs/remotes/origin/deleted")).toBe(before!);
  expect(await f.git(local, "rev-parse", "refs/tags/local-only")).toBe(before!);
  expect((await repo.readStatus()).head).toBe(before);
  await f.git(local, "config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main");
  expect((await repo.readStatus()).fetchWarnings.some((warning) => warning.includes("narrow"))).toBe(true);
  await f.git(local, "config", "remote.origin.fetch", "+refs/heads/*:refs/heads/*");
  await expect(repo.fetchAll()).rejects.toThrow("local branches");
  await f.git(local, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*");
  await f.git(local, "remote", "set-url", "origin", `${remote}/missing`);
  await expect(repo.fetchAll()).rejects.toThrow("Fetch failed");
});

test("fetch and pull refuse wildcard mappings into local branches without changing refs or configuration", async () => {
  const f = await setup();
  const { local, writer } = await f.paired();
  const repo = await GitRepository.open(local);
  const before = await f.git(local, "rev-parse", "HEAD");
  await f.git(local, "branch", "topic");
  const next = await f.commit(writer, "topic-next");
  await f.git(writer, "push", "origin", "HEAD:refs/heads/topic");
  const tracking = "+refs/heads/*:refs/remotes/origin/*";
  for (const mapping of [
    "+refs/*:refs/*", "+refs/hea*:refs/hea*", "+refs/heads*:refs/heads*",
    "+refs/heads/*:refs/*/topic", "refs/heads/topic:topic", "refs/heads/topic:refs/heads",
  ]) {
    await f.git(local, "config", "--replace-all", "remote.origin.fetch", tracking);
    await f.git(local, "config", "--add", "remote.origin.fetch", mapping);
    await expect(repo.fetchAll()).rejects.toThrow("local branches");
    await expect(repo.pull("ff-only")).rejects.toThrow("local branches");
    expect(await f.git(local, "rev-parse", "refs/heads/topic")).toBe(before);
    expect(await f.git(local, "rev-parse", "HEAD")).toBe(before);
    expect(await f.git(local, "config", "--get-all", "remote.origin.fetch")).toBe(`${tracking}\n${mapping}`);
  }
  await f.git(writer, "tag", "topic-tag", next);
  await f.git(writer, "push", "origin", "refs/tags/topic-tag");
  await f.git(local, "config", "--replace-all", "remote.origin.fetch", tracking);
  await f.git(local, "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*");
  await repo.fetchAll();
  expect(await f.git(local, "rev-parse", "refs/remotes/origin/topic")).toBe(next);
  expect(await f.git(local, "rev-parse", "refs/tags/topic-tag")).toBe(next);
  expect(await f.git(local, "rev-parse", "refs/heads/topic")).toBe(before);
});

test("ff-only fast-forwards under contrary ambient preferences and refuses divergence", async () => {
  const f = await setup();
  const { local, writer } = await f.paired();
  const repo = await GitRepository.open(local);
  await f.git(local, "config", "pull.rebase", "true");
  await f.git(local, "config", "pull.ff", "false");
  const next = await f.commit(writer, "remote-next");
  await f.git(writer, "push", "origin", "main");
  await repo.pull("ff-only");
  expect((await repo.readStatus()).head).toBe(next);
  const own = await f.commit(local, "own");
  await f.commit(writer, "remote-diverged");
  await f.git(writer, "push", "origin", "main");
  await expect(repo.pull("ff-only")).rejects.toThrow("Pull ff-only failed");
  expect((await repo.readStatus()).head).toBe(own);
  expect((await repo.readStatus()).operation).toBeNull();
});

test("pull respects an explicit skipped remote or local upstream, not ambient fetch.all", async () => {
  const f = await setup();
  const { local, writer } = await f.paired();
  const repo = await GitRepository.open(local);
  await f.git(local, "config", "remote.origin.skipFetchAll", "true");
  await f.git(local, "remote", "add", "unrelated", join(f.root, "missing"));
  await f.git(local, "config", "fetch.all", "true");
  const next = await f.commit(writer, "next");
  await f.git(writer, "push", "origin", "main");
  await repo.pull("ff-only");
  expect((await repo.readStatus()).head).toBe(next);
  await f.git(local, "switch", "-c", "local-upstream");
  const localNext = await f.commit(local, "local-next");
  await f.git(local, "switch", "main");
  await f.git(local, "config", "branch.main.remote", ".");
  await f.git(local, "config", "branch.main.merge", "refs/heads/local-upstream");
  await repo.pull("ff-only");
  expect((await repo.readStatus()).head).toBe(localNext);
});

for (const mode of ["merge", "rebase"] as const) test(`${mode} honors explicit policy over contrary ambient config`, async () => {
  const f = await setup();
  const { local, writer } = await f.paired();
  const repo = await GitRepository.open(local);
  await f.git(local, "config", "pull.ff", "only");
  await f.git(local, "config", "pull.rebase", mode === "merge" ? "true" : "false");
  await f.git(local, "config", "rebase.autoStash", "true");
  await f.git(local, "config", "merge.autoStash", "true");
  // Merge must permit a fast-forward, not create a needless merge commit.
  const fastForward = await f.commit(writer, "ff");
  await f.git(writer, "push", "origin", "main");
  await repo.pull(mode);
  expect((await repo.readStatus()).head).toBe(fastForward);
  const own = await f.commit(local, "own");
  const upstream = await f.commit(writer, "upstream");
  await f.git(writer, "push", "origin", "main");
  await repo.pull(mode);
  const status = await repo.readStatus();
  const details = await repo.readCommitDetails(status.head!);
  expect(details.parents).toEqual(mode === "merge" ? [own, upstream] : [upstream]);
  expect(status.dirty).toBe(false);
  expect(status.operation).toBeNull();
});

test("pull refuses untracked/index/worktree changes, detached HEAD and missing upstream", async () => {
  const f = await setup();
  const { local } = await f.paired();
  const repo = await GitRepository.open(local);
  await f.git(local, "config", "rebase.autoStash", "true");
  await f.git(local, "config", "merge.autoStash", "true");
  await writeFile(join(local, "untracked"), "keep me");
  await expect(repo.pull("merge")).rejects.toThrow("untracked changes");
  await f.git(local, "add", "untracked");
  await expect(repo.pull("rebase")).rejects.toThrow("untracked changes");
  await f.git(local, "commit", "-m", "track file");
  await writeFile(join(local, "untracked"), "modified");
  await expect(repo.pull("ff-only")).rejects.toThrow("untracked changes");
  expect(await readFile(join(local, "untracked"), "utf8")).toBe("modified");
  await f.git(local, "switch", "--detach");
  expect((await repo.readStatus()).branch).toBeNull();
  await expect(repo.pull("ff-only")).rejects.toThrow("attached");
  await f.git(local, "switch", "-c", "no-upstream");
  await expect(repo.pull("ff-only")).rejects.toThrow("upstream");
});

test("merge/rebase conflicts and existing operations remain intact, including linked worktree markers", async () => {
  const f = await setup();
  const { local, writer } = await f.paired();
  const repo = await GitRepository.open(local);
  await f.commit(local, "base", "local conflict");
  await f.commit(writer, "base", "upstream conflict");
  await f.git(writer, "push", "origin", "main");
  await expect(repo.pull("merge")).rejects.toThrow("merge state is preserved");
  const before = (await repo.readStatus()).head;
  expect((await repo.readStatus()).operation).toBe("merge");
  await expect(repo.pull("rebase")).rejects.toThrow("during merge");
  expect((await repo.readStatus()).head).toBe(before);
  // Only this fixture's explicit test action aborts; the backend never does.
  await f.git(local, "merge", "--abort");
  await expect(repo.pull("rebase")).rejects.toThrow("rebase state is preserved");
  expect((await repo.readStatus()).operation).toBe("rebase");
  await f.git(local, "rebase", "--abort");
  const linked = join(f.root, "linked worktree");
  await f.git(local, "worktree", "add", "-b", "linked", linked);
  const linkedRepo = await GitRepository.open(linked);
  const marker = await f.git(linked, "rev-parse", "--git-path", "CHERRY_PICK_HEAD");
  await writeFile(marker, `${before}\n`);
  expect((await linkedRepo.readStatus()).operation).toBe("cherry-pick");
  await expect(linkedRepo.pull("ff-only")).rejects.toThrow("during cherry-pick");
  await rm(marker);
  for (const [name, operation, directory] of [
    ["REVERT_HEAD", "revert", false], ["sequencer", "sequencer", true],
    ["BISECT_START", "bisect", false], ["rebase-apply", "rebase", true],
  ] as const) {
    const path = await f.git(linked, "rev-parse", "--git-path", name);
    if (directory) await mkdir(path); else await writeFile(path, `${before}\n`);
    expect((await linkedRepo.readStatus()).operation).toBe(operation);
    await rm(path, { recursive: directory });
  }
});

test("terminal presentation escapes controls and bidi without changing multiline content", () => {
  expect(terminalSafe("normal\n\t\r\u001b[31m\u0000\u007f\u0085\u202eabc\u2066")).toBe("normal\n\\t\\r\\x1b[31m\\x00\\x7f\\x85\\u202eabc\\u2066");
});
