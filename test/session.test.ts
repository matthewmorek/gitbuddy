import { describe, expect, test } from "bun:test";
import { RepositorySession, type Repository, type SessionClock } from "../src/repository-session";
import type { CommitDetails, CommitSummary, RepositoryStatus } from "../src/git";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const row = (sha: string): CommitSummary => ({ sha, subject: sha, author: "Author", authoredAt: "2026-11-12T12:00:00Z", labels: [] });
const detail = (sha: string): CommitDetails => ({ ...row(sha), authorEmail: "a@example.test", message: sha, committer: "Author", committerEmail: "a@example.test", committedAt: "2026-11-12T12:00:00Z", parents: [], files: [], comparison: "Root commit" });
const status: RepositoryStatus = { branch: "main", head: "a", upstream: "origin/main", dirty: false, operation: null, remotes: ["origin"], fetchWarnings: [] };
function fixture() {
  let rows = [row("a"), row("b")];
  const historyReads: Array<[offset: number, limit: number]> = [];
  const repo: Repository = {
    root: "/example",
    readStatus: async () => status,
    snapshot: async () => ({ roots: rows.map(row => row.sha), labels: {} }),
    readHistoryPage: async (_snapshot, offset, limit = 200) => {
      historyReads.push([offset, limit]);
      return rows.slice(offset, offset + limit);
    },
    readCommitDetails: async sha => detail(sha),
    fetchAll: async () => {}, pull: async () => {},
  };
  let tick: (() => void) | undefined;
  const clock: SessionClock = { now: () => 1234, every: callback => { tick = callback; return () => { tick = undefined; }; } };
  return { repo, clock, historyReads, tick: () => tick?.(), setRows: (next: CommitSummary[]) => { rows = next; } };
}

describe("repository session", () => {
  test("the default total cap bounds initial history, navigation, scope and fetch refreshes without lookahead", async () => {
    const f = fixture();
    f.setRows(Array.from({ length: 500 }, (_, index) => row(String(index))));
    const session = new RepositorySession(f.repo, f.clock);
    const refreshing = session.refresh();
    expect(session.state.loading).toBe(true);
    expect(session.state.loadingMore).toBe(false);
    await refreshing;
    expect(session.state.loading).toBe(false);
    expect(session.state.rows).toHaveLength(100);
    expect(session.state.hasMore).toBe(false);
    await session.select("99");
    await session.move(1);
    await session.loadMore();
    expect(session.state.selected).toBe("99");
    expect(f.historyReads).toEqual([[0, 100]]);
    await session.refresh("current");
    await session.fetch();
    expect(session.state.scope).toBe("current");
    expect(session.state.rows).toHaveLength(100);
    expect(session.state.hasMore).toBe(false);
    expect(f.historyReads).toEqual([[0, 100], [0, 100], [0, 100]]);
  });

  test("increasing enables bounded pages and lowering immediately retains out-of-view selection and details", async () => {
    const f = fixture();
    f.setRows(Array.from({ length: 600 }, (_, index) => row(String(index))));
    const session = new RepositorySession(f.repo, f.clock);
    await session.refresh();
    await session.select("99");
    await session.setMaxCommits(450);
    expect(session.state.rows).toHaveLength(200);
    expect(session.state.hasMore).toBe(true);
    expect(session.state.selected).toBe("99");
    expect(session.state.details?.sha).toBe("99");
    await session.loadMore();
    expect(session.state.rows).toHaveLength(400);
    expect(session.state.hasMore).toBe(true);
    await session.loadMore();
    expect(session.state.rows).toHaveLength(450);
    expect(session.state.hasMore).toBe(false);
    expect(f.historyReads).toEqual([[0, 100], [0, 201], [200, 201], [400, 50]]);
    await session.setMaxCommits(450);
    await session.loadMore();
    expect(f.historyReads).toHaveLength(4);
    await session.refresh();
    expect(f.historyReads.at(-1)).toEqual([0, 450]);
    expect(session.state.rows).toHaveLength(450);
    await session.refresh("current");
    expect(session.state.rows).toHaveLength(200);
    expect(f.historyReads.at(-1)).toEqual([0, 201]);
    await session.loadMore();
    await session.loadMore();
    await session.fetch();
    expect(session.state.rows).toHaveLength(450);
    expect(session.state.hasMore).toBe(false);
    expect(f.historyReads.at(-1)).toEqual([0, 450]);
    await session.select("440");
    const reads = f.historyReads.length;
    const lowering = session.setMaxCommits(75);
    expect(session.state.rows).toHaveLength(75);
    expect(session.state.selected).toBe("440");
    expect(session.state.details?.sha).toBe("440");
    expect(session.state.hasMore).toBe(false);
    await lowering;
    expect(f.historyReads).toHaveLength(reads);
    await session.move(1);
    expect(session.state.selected).toBe("0");
  });

  test("lowering retires pending pagination and navigation; later paging still works", async () => {
    const f = fixture();
    const rows = Array.from({ length: 600 }, (_, index) => row(String(index)));
    f.setRows(rows);
    const session = new RepositorySession(f.repo, f.clock, 500);
    await session.refresh();
    await session.select("199");
    const page = deferred<CommitSummary[]>();
    const readPage = f.repo.readHistoryPage;
    f.repo.readHistoryPage = async (_snapshot, offset) => offset ? page.promise : rows.slice(0, 201);
    const moving = session.move(1);
    expect(session.state.loadingMore).toBe(true);
    const lowering = session.setMaxCommits(150);
    expect(session.state.loadingMore).toBe(false);
    expect(session.state.rows).toHaveLength(150);
    expect(session.state.hasMore).toBe(false);
    await lowering;
    page.resolve(rows.slice(200, 401));
    await moving;
    expect(session.state.loadingMore).toBe(false);
    expect(session.state.rows).toHaveLength(150);
    expect(session.state.selected).toBe("199");
    expect(session.state.details?.sha).toBe("199");
    f.repo.readHistoryPage = readPage;
    await session.setMaxCommits(350);
    await session.loadMore();
    expect(session.state.rows).toHaveLength(350);
    expect(session.state.hasMore).toBe(false);
    expect(f.historyReads.at(-1)).toEqual([200, 150]);
  });

  test("a retired page cannot release a newer generation's pending pagination", async () => {
    const f = fixture();
    const rows = Array.from({ length: 600 }, (_, index) => row(String(index)));
    f.setRows(rows);
    const session = new RepositorySession(f.repo, f.clock, 500);
    await session.refresh();
    const oldPage = deferred<CommitSummary[]>();
    const newPage = deferred<CommitSummary[]>();
    let reads = 0;
    f.repo.readHistoryPage = () => ++reads === 1 ? oldPage.promise : newPage.promise;
    const oldPaging = session.loadMore();
    expect(session.state.loadingMore).toBe(true);
    await session.setMaxCommits(300);
    expect(session.state.loadingMore).toBe(false);
    const newPaging = session.loadMore();
    expect(session.state.loadingMore).toBe(true);
    oldPage.resolve(rows.slice(200, 401));
    await oldPaging;
    expect(session.state.loadingMore).toBe(true);
    expect(session.state.rows).toHaveLength(200);
    await session.loadMore();
    expect(reads).toBe(2);
    newPage.resolve(rows.slice(200, 301));
    await newPaging;
    expect(session.state.loadingMore).toBe(false);
    expect(session.state.rows).toHaveLength(300);
    expect(session.state.hasMore).toBe(false);
  });

  test("lowering during a scope refresh caps the replacement and rejects the late old history", async () => {
    const f = fixture();
    const rows = Array.from({ length: 500 }, (_, index) => row(String(index)));
    f.setRows(rows);
    const session = new RepositorySession(f.repo, f.clock, 400);
    await session.refresh();
    await session.select("199");
    const oldPage = deferred<CommitSummary[]>();
    const started = deferred<void>();
    const readPage = f.repo.readHistoryPage;
    let first = true;
    f.repo.readHistoryPage = (snapshot, offset, limit) => {
      if (!first) return readPage(snapshot, offset, limit);
      first = false;
      started.resolve();
      return oldPage.promise;
    };
    const refreshing = session.refresh("current");
    await started.promise;
    await session.setMaxCommits(80);
    expect(session.state.scope).toBe("current");
    expect(session.state.rows).toHaveLength(80);
    expect(session.state.selected).toBe("199");
    expect(session.state.details?.sha).toBe("199");
    expect(f.historyReads.at(-1)).toEqual([0, 80]);
    oldPage.resolve(rows);
    await refreshing;
    expect(session.state.rows).toHaveLength(80);
    expect(session.state.hasMore).toBe(false);
    await session.refresh();
    expect(session.state.rows).toHaveLength(80);
  });

  test("a retired refresh awaiting its snapshot never issues an obsolete surplus history read", async () => {
    const f = fixture();
    f.setRows(Array.from({ length: 500 }, (_, index) => row(String(index))));
    const snapshot = deferred<Awaited<ReturnType<Repository["snapshot"]>>>();
    const readSnapshot = f.repo.snapshot;
    let first = true;
    f.repo.snapshot = scope => {
      if (!first) return readSnapshot(scope);
      first = false;
      return snapshot.promise;
    };
    const session = new RepositorySession(f.repo, f.clock, 400);
    const refreshing = session.refresh("current");
    await session.setMaxCommits(60);
    snapshot.resolve(await readSnapshot("current"));
    await refreshing;
    expect(session.state.scope).toBe("current");
    expect(session.state.rows).toHaveLength(60);
    expect(f.historyReads).toEqual([[0, 60]]);
  });

  test("public commit caps require positive safe integers, without an arbitrary upper bound", async () => {
    const f = fixture();
    f.setRows(Array.from({ length: 500 }, (_, index) => row(String(index))));
    const session = new RepositorySession(f.repo, f.clock);
    await session.refresh();
    for (const limit of [0, -1, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
      expect(() => new RepositorySession(f.repo, f.clock, limit)).toThrow("positive safe integer");
      await expect(session.setMaxCommits(limit)).rejects.toThrow("positive safe integer");
    }
    expect(session.state.rows).toHaveLength(100);
    expect(f.historyReads).toEqual([[0, 100]]);
    await session.setMaxCommits(Number.MAX_SAFE_INTEGER);
    expect(session.state.rows).toHaveLength(200);
    expect(session.state.hasMore).toBe(true);
    expect(f.historyReads.at(-1)).toEqual([0, 201]);
    await session.setMaxCommits(1);
    expect(session.state.rows).toHaveLength(1);
    expect(session.state.hasMore).toBe(false);
  });

  test("refresh preserves selected SHA and retained out-of-view details", async () => {
    const f = fixture();
    const session = new RepositorySession(f.repo, f.clock);
    await session.refresh();
    await session.select("b");
    f.setRows([row("new"), row("a"), row("b")]);
    await session.refresh();
    expect(session.state.selected).toBe("b");
    f.setRows([row("new"), row("a")]);
    await session.refresh();
    expect(session.state.details?.sha).toBe("b");
    expect(session.state.rows.some(row => row.sha === "b")).toBe(false);
    await session.move(1);
    expect(session.state.selected).toBe("new");
  });

  test("a late detail response cannot replace the selected commit", async () => {
    const f = fixture();
    const old = deferred<CommitDetails>();
    f.repo.readCommitDetails = sha => sha === "a" ? old.promise : Promise.resolve(detail(sha));
    const session = new RepositorySession(f.repo, f.clock);
    const selectingOld = session.select("a");
    await session.select("b");
    old.resolve(detail("a"));
    await selectingOld;
    expect(session.state.selected).toBe("b");
    expect(session.state.details?.sha).toBe("b");
  });

  test("a stale history page cannot enter a newer refresh generation", async () => {
    const f = fixture();
    const page = deferred<CommitSummary[]>();
    f.setRows(Array.from({ length: 201 }, (_, index) => row(String(index))));
    const session = new RepositorySession(f.repo, f.clock, 500);
    await session.refresh();
    f.repo.readHistoryPage = async (_snapshot, offset) => offset ? page.promise : [row("new")];
    const paging = session.loadMore();
    expect(session.state.loadingMore).toBe(true);
    await session.refresh();
    expect(session.state.loadingMore).toBe(false);
    page.resolve([row("stale")]);
    await paging;
    expect(session.state.rows.map(row => row.sha)).toEqual(["new"]);
    expect(session.state.loadingMore).toBe(false);
  });

  test("a failed page clears loadingMore and permits a successful retry", async () => {
    const f = fixture();
    f.setRows(Array.from({ length: 350 }, (_, index) => row(String(index))));
    const session = new RepositorySession(f.repo, f.clock, 350);
    await session.refresh();
    const readPage = f.repo.readHistoryPage;
    const page = deferred<CommitSummary[]>();
    f.repo.readHistoryPage = () => page.promise;
    const paging = session.loadMore();
    expect(session.state.loadingMore).toBe(true);
    page.reject(new Error("page failed"));
    await paging;
    expect(session.state.loadingMore).toBe(false);
    expect(session.state.rows).toHaveLength(200);
    expect(session.state.error).toBe("page failed");
    f.repo.readHistoryPage = readPage;
    await session.loadMore();
    expect(session.state.rows).toHaveLength(350);
    expect(session.state.loadingMore).toBe(false);
    expect(session.state.hasMore).toBe(false);
  });

  for (const superseding of ["move", "select", "refresh", "close"] as const) test(`pending navigation cannot override a newer ${superseding}`, async () => {
    const f = fixture();
    const page = deferred<CommitSummary[]>();
    f.setRows(Array.from({ length: 201 }, (_, index) => row(String(index))));
    const session = new RepositorySession(f.repo, f.clock, 500);
    await session.refresh();
    await session.select(superseding === "close" ? "198" : "199");
    f.repo.readHistoryPage = async (_snapshot, offset) => offset ? page.promise : [row("new")];
    const moving = session.move(superseding === "close" ? 2 : 1);
    expect(session.state.loadingMore).toBe(true);
    if (superseding === "move") await session.move(-1);
    if (superseding === "select") await session.select("10");
    if (superseding === "refresh") await session.refresh();
    if (superseding === "close") await session.close();
    if (superseding === "close" || superseding === "refresh") expect(session.state.loadingMore).toBe(false);
    const selected = session.state.selected;
    if (superseding === "close") page.reject(new Error("retired page failed"));
    else page.resolve([row("200"), row("201")]);
    await moving;
    expect(session.state.loadingMore).toBe(false);
    expect(session.state.error).toBe(null);
    expect(session.state.selected).toBe(selected);
    expect(session.state.details?.sha ?? null).toBe(selected);
    if (superseding !== "close") {
      await session.move(1);
      expect(session.state.selected).toBe(superseding === "move" ? "199" : superseding === "select" ? "11" : "new");
    }
  });

  test("initial fetch and repeated ticks never overlap; quit waits and stops scheduling", async () => {
    const f = fixture();
    const pending = deferred<void>();
    let fetches = 0;
    let pulls = 0;
    f.repo.fetchAll = async () => { fetches++; await pending.promise; };
    f.repo.pull = async () => { pulls++; };
    const session = new RepositorySession(f.repo, f.clock);
    await session.refresh();
    session.start(true, 5);
    f.tick(); f.tick();
    await session.pull("rebase");
    expect(fetches).toBe(1);
    expect(pulls).toBe(0);
    let closed = false;
    const closing = session.close().then(() => { closed = true; });
    await Promise.resolve();
    expect(closed).toBe(false);
    pending.resolve();
    await closing;
    f.tick();
    expect(fetches).toBe(1);
    expect(session.state.closing).toBe(true);
  });

  test("failed fetch refreshes partial changes but keeps an error and last-success time", async () => {
    const f = fixture();
    const session = new RepositorySession(f.repo, f.clock);
    await session.refresh();
    await session.fetch();
    f.repo.fetchAll = async () => { f.setRows([row("partial")]); throw new Error("second remote failed"); };
    await session.fetch();
    expect(session.state.rows[0]?.sha).toBe("partial");
    expect(session.state.error).toContain("second remote failed");
    expect(session.state.fetchFailed).toBe(true);
    expect(session.state.lastFetch).toBe(1234);
  });

  test("disabling auto-fetch retires ticks without cancelling a running pull", async () => {
    const f = fixture();
    let fetches = 0;
    f.repo.fetchAll = async () => { fetches++; };
    const session = new RepositorySession(f.repo, f.clock);
    await session.refresh();
    session.start(false, 5);
    f.tick();
    expect(fetches).toBe(0);
    session.configure(true, 1);
    session.configure(false, 1);
    f.tick();
    expect(fetches).toBe(0);
    await session.close();
  });
});
