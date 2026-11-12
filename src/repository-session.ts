import type { CommitDetails, CommitSummary, GitRepository, HistoryScope, HistorySnapshot, PullMode, RepositoryStatus } from "./git";

export type Repository = Pick<GitRepository, "root" | "readStatus" | "snapshot" | "readHistoryPage" | "readCommitDetails" | "fetchAll" | "pull">;
export interface SessionState {
  status: RepositoryStatus | null;
  scope: HistoryScope;
  rows: CommitSummary[];
  selected: string | null;
  details: CommitDetails | null;
  detailsLoading: boolean;
  loading: boolean;
  loadingMore: boolean;
  hasMore: boolean;
  operation: string | null;
  error: string | null;
  lastFetch: number | null;
  fetchFailed: boolean;
  closing: boolean;
}

export interface SessionClock {
  now(): number;
  every(callback: () => void, milliseconds: number): () => void;
}
const clock: SessionClock = {
  now: Date.now,
  every(callback, milliseconds) {
    const timer = setInterval(callback, milliseconds);
    return () => clearInterval(timer);
  },
};

function validateMaxCommits(limit: number): void {
  if (!Number.isSafeInteger(limit) || limit < 1) throw new Error("maxCommits must be a positive safe integer");
}

/** Owns refresh generations, selection and the one app-initiated mutation slot.
 * External Git clients remain outside this serialization; Git owns its locks. */
export class RepositorySession {
  state: SessionState = {
    status: null, scope: "all", rows: [], selected: null, details: null,
    detailsLoading: false, loading: false, loadingMore: false, hasMore: false, operation: null,
    error: null, lastFetch: null, fetchFailed: false, closing: false,
  };
  private listeners = new Set<(state: SessionState) => void>();
  private generation = 0;
  private detailRequest = 0;
  private navigationRequest = 0;
  private snapshot: HistorySnapshot | null = null;
  private paging: number | null = null;
  private refreshScope: HistoryScope | null = null;
  private mutation: Promise<void> | null = null;
  private stopTimer?: () => void;
  private started = false;

  constructor(readonly repository: Repository, private readonly time: SessionClock = clock, private maxCommits = 100) {
    validateMaxCommits(maxCommits);
  }

  subscribe(listener: (state: SessionState) => void): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  }

  private update(patch: Partial<SessionState>) {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }

  reportError(error: unknown) {
    this.update({ error: error instanceof Error ? error.message : String(error) });
  }

  clearError() { this.update({ error: null }); }

  /** Sets the total per-view cap (a positive safe integer), not the page size.
   * Trimming is immediate; retained selection/details may remain outside history. */
  async setMaxCommits(limit: number): Promise<void> {
    validateMaxCommits(limit);
    if (limit === this.maxCommits || this.state.closing) return;
    const reload = this.state.loading || (limit > this.maxCommits && this.snapshot !== null);
    const scope = this.refreshScope ?? this.state.scope;
    this.maxCommits = limit;
    ++this.generation;
    this.paging = null;
    this.refreshScope = null;
    const rows = this.state.rows.slice(0, limit);
    this.update({ rows, hasMore: rows.length < limit && this.state.hasMore, loading: false, loadingMore: false });
    if (reload) await this.refresh(scope);
  }

  async refresh(scope: HistoryScope = this.state.scope): Promise<void> {
    if (this.state.closing) return;
    const generation = ++this.generation;
    this.paging = null;
    this.refreshScope = scope;
    const changedScope = scope !== this.state.scope;
    const count = Math.min(this.maxCommits, changedScope ? 200 : Math.max(200, this.state.rows.length));
    this.update({ loading: true, loadingMore: false });
    try {
      const [status, snapshot] = await Promise.all([
        this.repository.readStatus(), this.repository.snapshot(scope),
      ]);
      if (generation !== this.generation || this.state.closing) return;
      const page = await this.repository.readHistoryPage(snapshot, 0, count + Number(count < this.maxCommits));
      if (generation !== this.generation || this.state.closing) return;
      this.snapshot = snapshot;
      this.refreshScope = null;
      const rows = page.slice(0, count);
      // Keep an out-of-view SHA and its details until explicit navigation.
      const selected = this.state.selected ?? rows[0]?.sha ?? null;
      this.update({ status, rows, selected, scope, loading: false, hasMore: count < this.maxCommits && page.length > count });
      if (selected && this.state.details?.sha !== selected) void this.select(selected);
    } catch (error) {
      if (generation === this.generation && !this.state.closing) {
        this.refreshScope = null;
        this.update({ loading: false });
        this.reportError(error); // Retain the previous usable snapshot on failure.
      }
    }
  }

  async loadMore(): Promise<void> {
    if (!this.snapshot || !this.state.hasMore || this.paging !== null || this.state.loading || this.state.closing) return;
    const generation = this.generation;
    const offset = this.state.rows.length;
    const count = Math.min(200, this.maxCommits - offset);
    if (count < 1) return;
    this.paging = generation;
    this.update({ loadingMore: true });
    try {
      const page = await this.repository.readHistoryPage(this.snapshot, offset, count + Number(offset + count < this.maxCommits));
      if (generation !== this.generation || this.state.closing) return;
      this.update({ rows: [...this.state.rows, ...page.slice(0, count)], hasMore: offset + count < this.maxCommits && page.length > count });
    } catch (error) {
      if (generation === this.generation && !this.state.closing) this.reportError(error);
    } finally {
      if (this.paging === generation) {
        this.paging = null;
        this.update({ loadingMore: false });
      }
    }
  }

  async select(sha: string): Promise<void> {
    if (this.state.closing) return;
    ++this.navigationRequest;
    const request = ++this.detailRequest;
    this.update({ selected: sha, details: null, detailsLoading: true });
    try {
      const details = await this.repository.readCommitDetails(sha);
      if (request === this.detailRequest && !this.state.closing) this.update({ details, detailsLoading: false });
    } catch (error) {
      if (request === this.detailRequest && !this.state.closing) {
        this.update({ detailsLoading: false });
        this.reportError(error);
      }
    }
  }

  async move(delta: number): Promise<void> {
    if (!this.state.rows.length || this.state.closing) return;
    const request = ++this.navigationRequest;
    const generation = this.generation;
    let index = this.state.rows.findIndex(row => row.sha === this.state.selected);
    if (index < 0) index = delta > 0 ? -1 : this.state.rows.length;
    if (index + delta >= this.state.rows.length && this.state.hasMore) await this.loadMore();
    // A newer navigation or refresh owns selection after an asynchronous page.
    if (request !== this.navigationRequest || generation !== this.generation || this.state.closing) return;
    const next = this.state.rows[Math.max(0, Math.min(this.state.rows.length - 1, index + delta))];
    if (next && next.sha !== this.state.selected) void this.select(next.sha);
  }

  /** Called after the first local frame; configuration changes never queue fetches. */
  start(autoFetch: boolean, intervalMinutes: number) {
    if (this.started || this.state.closing) return;
    this.started = true;
    this.configure(autoFetch, intervalMinutes);
    if (autoFetch) void this.fetch();
  }

  configure(autoFetch: boolean, intervalMinutes: number) {
    this.stopTimer?.();
    this.stopTimer = undefined;
    if (!this.started || !autoFetch || this.state.closing) return;
    this.stopTimer = this.time.every(() => { void this.fetch(); }, intervalMinutes * 60_000);
  }

  fetch(): Promise<void> {
    if (!this.state.status?.remotes.length) return Promise.resolve();
    return this.mutate("Fetching", async () => {
      try {
        await this.repository.fetchAll();
        this.update({ lastFetch: this.time.now(), fetchFailed: false });
      } catch (error) {
        this.update({ fetchFailed: true });
        throw error;
      }
    });
  }

  pull(mode: PullMode): Promise<void> {
    return this.mutate(`Pulling (${mode})`, () => this.repository.pull(mode));
  }

  private mutate(label: string, run: () => Promise<void>): Promise<void> {
    // Timer ticks and repeated commands are dropped, not queued behind a pull.
    if (this.mutation || this.state.closing) return Promise.resolve();
    this.update({ operation: label });
    this.mutation = (async () => {
      try { await run(); }
      catch (error) { this.reportError(error); }
      finally {
        // Even a failed multi-remote fetch can update some refs.
        await this.refresh();
        this.update({ operation: null });
        this.mutation = null;
      }
    })();
    return this.mutation;
  }

  async close(): Promise<void> {
    this.stopTimer?.();
    this.stopTimer = undefined;
    ++this.generation;
    ++this.detailRequest;
    this.paging = null;
    this.update({ closing: true, loading: false, loadingMore: false, detailsLoading: false });
    await this.mutation; // Never kill or roll back an integrating pull on normal quit.
  }
}
