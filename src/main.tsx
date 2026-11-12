import { createCliRenderer, CliRenderEvents } from "@opentui/core";
import { render } from "@opentui/solid";
import { App } from "./app";
import { GitRepository, terminalSafe } from "./git";
import { RepositorySession } from "./repository-session";
import { SettingsStore } from "./settings";
import { ThemeCatalog } from "./theme";
import manifest from "../package.json";

declare const GITBUDDY_BUNDLED_NOTICES: string;
const args = process.argv.slice(2);
if (args.includes("--version")) {
  console.log(`gitbuddy ${manifest.version}`);
} else if (args.includes("--licenses")) {
  console.log(typeof GITBUDDY_BUNDLED_NOTICES === "string" ? GITBUDDY_BUNDLED_NOTICES : "See THIRD_PARTY_NOTICES.md and the installed dependencies' license files. Standalone builds embed this inventory.");
} else if (args.includes("--help") || args.includes("-h")) {
  console.log("Usage: gitbuddy [repository-path]\n       gitbuddy --help | --version | --licenses\n\nKeyboard-first Git history. Ctrl+P opens commands and settings.\nRequires Git and a terminal. Auto-fetch defaults to every 5 minutes while open.");
} else if (args.length > 1 || args[0]?.startsWith("-")) {
  console.error("Usage: gitbuddy [repository-path] | --help | --version");
  process.exitCode = 2;
} else {
  let renderer: Awaited<ReturnType<typeof createCliRenderer>> | undefined;
  let session: RepositorySession | undefined;
  try {
    if (!process.stdin.isTTY || !process.stdout.isTTY) throw new Error("Gitbuddy needs an interactive terminal. Use --help for usage.");
    const repository = await GitRepository.open(args[0] ?? process.cwd());
    const settings = await SettingsStore.load();
    const themes = await ThemeCatalog.load(settings.directory);
    session = new RepositorySession(repository, undefined, settings.value.maxCommits);
    renderer = await createCliRenderer({ exitOnCtrlC: false, exitSignals: [], useMouse: false });
    // Publish loading before mounting instead of hiding the initial Git reads.
    const initialRefresh = session.refresh();
    let quitting: Promise<void> | undefined;
    const quit = () => quitting ??= (async () => {
      await session!.close();
      renderer!.destroy();
      await renderer!.closed;
    })();
    const interrupt = () => { void quit(); };
    process.on("SIGINT", interrupt);
    process.on("SIGTERM", interrupt);
    await render(() => <App session={session!} settings={settings} themes={themes} quit={quit} />, renderer);
    void initialRefresh.then(() => {
      if (renderer!.isDestroyed || session!.state.closing) return;
      // A frame already being presented may still contain the loading state.
      // Wait for a fresh local-history frame before any automatic network work.
      const completedAtFrame = renderer!.frameId;
      const start = () => {
        if (renderer!.frameId <= completedAtFrame || session!.state.loading) return;
        renderer!.off(CliRenderEvents.FRAME, start);
        if (!renderer!.isDestroyed && !session!.state.closing) session!.start(settings.value.autoFetch, settings.value.fetchIntervalMinutes);
      };
      renderer!.on(CliRenderEvents.FRAME, start);
      renderer!.requestRender();
    });
    await renderer.closed;
    process.off("SIGINT", interrupt);
    process.off("SIGTERM", interrupt);
  } catch (error) {
    await session?.close();
    renderer?.destroy();
    await renderer?.closed;
    console.error(`gitbuddy: ${terminalSafe(error instanceof Error ? error.message : String(error))}`);
    process.exitCode = 1;
  }
}
