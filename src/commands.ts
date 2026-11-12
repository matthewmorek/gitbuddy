import type { PullMode } from "./git";
import type { RepositorySession } from "./repository-session";

export interface AppCommand {
  id: string;
  title: string;
  category?: string;
  keywords?: string;
  keys?: string[];
  unavailable?: () => string | undefined;
  run(): void | Promise<void>;
}

/** Shared by the keymap and palette: availability is rechecked at execution. */
export function repositoryCommands(session: RepositorySession): AppCommand[] {
  const busy = () => session.state.closing ? "Waiting to quit" : session.state.operation ? "Another Git operation is running" : undefined;
  const pullReason = () => {
    const status = session.state.status;
    return busy() ?? (!status ? "Repository is loading" :
      !status.branch ? "HEAD is detached" : !status.head ? "No commits yet" :
      !status.upstream ? "Current branch has no upstream" : status.operation ? `Resolve ${status.operation} externally first` :
      status.dirty ? "Commit or remove changes externally first (including untracked files)" : undefined);
  };
  const pull = (mode: PullMode, title: string): AppCommand => ({
    id: `git.pull.${mode}`, title, category: "Git", unavailable: pullReason, run: () => session.pull(mode),
  });
  return [
    { id: "git.fetch", title: "Fetch now", category: "Git", unavailable: () => busy() ?? (!session.state.status?.remotes.length ? "No remotes configured" : undefined), run: () => session.fetch() },
    pull("ff-only", "Pull — fast-forward only (default)"),
    pull("merge", "Pull — merge (allow fast-forward)"),
    pull("rebase", "Pull — rebase local commits"),
    { id: "history.refresh", title: "Refresh local view", category: "History", keys: ["r"], run: () => session.refresh() },
    { id: "history.all", title: "All branches and tags", category: "History", run: () => session.refresh("all") },
    { id: "history.current", title: "Current branch", category: "History", run: () => session.refresh("current") },
  ];
}
