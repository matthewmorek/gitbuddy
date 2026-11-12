import { createEffect, createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import { useRenderer, useTerminalDimensions } from "@opentui/solid";
import { BorderChars, CliRenderEvents, RGBA, TextAttributes, type BoxRenderable, type Renderable, type ScrollBoxRenderable } from "@opentui/core";
import { createDefaultOpenTuiKeymap } from "@opentui/keymap/opentui";
import fuzzysort from "fuzzysort";
import { basename } from "node:path";
import { repositoryCommands, type AppCommand } from "./commands";
import { terminalSafe, type CommitDetails } from "./git";
import type { RepositorySession } from "./repository-session";
import type { Settings, SettingsStore } from "./settings";
import type { ThemeCatalog } from "./theme";
import { Spinner } from "./spinner";

type Dialog = "commands" | "settings" | "theme" | "mode" | "interval" | "maxCommits" | "error";
const line = (text: string) => terminalSafe(text).replace(/\n/g, "↵");
const selectionEdge = { ...BorderChars.single, vertical: "▌" };
const brightness = (color: RGBA) => color.r * 0.2126 + color.g * 0.7152 + color.b * 0.0722;

export function App(props: { session: RepositorySession; settings: SettingsStore; themes: ThemeCatalog; quit(): Promise<void> }) {
  const renderer = useRenderer();
  const dimensions = useTerminalDimensions();
  const [state, setState] = createSignal(props.session.state);
  const [preferences, setPreferences] = createSignal({ ...props.settings.value });
  const [settingsWarning, setSettingsWarning] = createSignal(props.settings.warning);
  const [systemMode, setSystemMode] = createSignal<"light" | "dark">(renderer.themeMode ?? "dark");
  const [pane, setPane] = createSignal<"history" | "details">("history");
  const [dialog, setDialog] = createSignal<Dialog | null>(null);
  const [query, setQuery] = createSignal("");
  const [choice, setChoice] = createSignal(0);
  const [top, setTop] = createSignal(0);
  let historyBox: BoxRenderable | undefined;
  let detailBox: ScrollBoxRenderable | undefined;
  let errorBox: ScrollBoxRenderable | undefined;
  let restoreFocus: Renderable | null = null;
  let previousRows = state().rows;
  const narrow = () => dimensions().width < 100;
  const theme = createMemo(() => props.themes.resolve(preferences().theme,
    preferences().colorMode === "system" ? systemMode() : preferences().colorMode as "light" | "dark"));
  // Selection deliberately uses the theme's accent, not neutral action text.
  const accent = () => theme().hue.accent[200];
  const backdrop = createMemo(() => {
    const shade = Object.values(theme().hue.neutral).reduce((darkest, color) => brightness(color) < brightness(darkest) ? color : darkest);
    return RGBA.fromValues(shade.r, shade.g, shade.b, 0.25);
  });
  const panelColors = createMemo(() => {
    const colors = [theme().background.base, theme().background.raised.base].sort((a, b) => brightness(a) - brightness(b));
    // Raised surfaces reverse contrast in some light themes. Always use the
    // darker semantic surface for focus, rather than assuming a mode's ordering.
    let inactive = colors[1]!;
    // A flat black custom palette has no darker color; lift only its inactive
    // surface so the focused panel can remain black and visibly distinct.
    if (brightness(inactive) === 0) inactive = RGBA.fromValues(0.04, 0.04, 0.04, inactive.a);
    const active = brightness(colors[0]!) < brightness(inactive) ? colors[0]! :
      RGBA.fromValues(inactive.r * 0.9, inactive.g * 0.9, inactive.b * 0.9, inactive.a);
    return { active, inactive };
  });
  const panelBackground = (name: "history" | "details") => pane() === name ? panelColors().active : panelColors().inactive;
  const warnings = createMemo(() => {
    theme();
    return [settingsWarning(), ...props.themes.warnings, ...(state().status?.fetchWarnings ?? [])].filter(Boolean).join("\n");
  });
  const notices = () => [state().error, warnings()].filter(Boolean).join("\n\n");
  const footerHeight = () => notices() ? 3 : 2;
  // Outer/header/panel padding and history heading+gap take eight rows.
  const visibleCount = () => Math.max(1, Math.floor((dimensions().height - 8 - footerHeight()) / 2));
  const activity = () => state().operation ? `${state().operation}...` : state().loading ? "Loading history..." :
    state().loadingMore ? "Loading more commits..." : null;
  const pendingDetails = createMemo(() => state().detailsLoading ? state().selected : null);
  const [slowDetails, setSlowDetails] = createSignal<string | null>(null);
  // Selection reads are local, not fetches. Avoid flashing global activity on
  // each keystroke; only a slow read gets feedback beside the affected content.
  createEffect(() => {
    const sha = pendingDetails();
    setSlowDetails(null);
    if (!sha) return;
    const timer = renderer.clock.setTimeout(() => setSlowDetails(sha), 150);
    onCleanup(() => renderer.clock.clearTimeout(timer));
  });
  const detailsActivity = () => pendingDetails() !== null && slowDetails() === pendingDetails();

  const open = (kind: Dialog) => {
    if (!dialog()) restoreFocus = renderer.currentFocusedRenderable ?? null;
    setQuery(kind === "interval" ? String(preferences().fetchIntervalMinutes) : kind === "maxCommits" ? String(preferences().maxCommits) : "");
    setChoice(0);
    setDialog(kind);
  };
  const close = () => {
    setDialog(null);
    queueMicrotask(() => {
      if (dialog()) return; // A command may have replaced the palette with a picker.
      if (restoreFocus && !restoreFocus.isDestroyed) restoreFocus.focus();
      else (pane() === "history" ? historyBox : detailBox)?.focus();
    });
  };
  const updateSettings = async (patch: Partial<Settings>) => {
    try {
      const saved = props.settings.update(patch);
      setPreferences({ ...props.settings.value });
      props.session.configure(props.settings.value.autoFetch, props.settings.value.fetchIntervalMinutes);
      void props.session.setMaxCommits(props.settings.value.maxCommits).catch(error => props.session.reportError(error));
      await saved;
    } catch (error) { props.session.reportError(error); }
    finally {
      setPreferences({ ...props.settings.value });
      setSettingsWarning(props.settings.warning);
      props.session.configure(props.settings.value.autoFetch, props.settings.value.fetchIntervalMinutes);
    }
  };
  const settingsCommands: AppCommand[] = [
    { id: "settings.fetch", title: "Automatic fetch enabled", category: "Settings › Fetch", keywords: "background periodic", run: () => updateSettings({ autoFetch: !preferences().autoFetch }) },
    { id: "settings.interval", title: "Automatic fetch interval", category: "Settings › Fetch", keywords: "minutes background periodic", run: () => open("interval") },
    { id: "settings.maxCommits", title: "Maximum commits", category: "Settings › History", keywords: "max number load log limit", run: () => open("maxCommits") },
    { id: "settings.theme", title: "Theme", category: "Settings › Appearance", keywords: "colors", run: () => open("theme") },
    { id: "settings.mode", title: "Color mode", category: "Settings › Appearance", keywords: "dark light system", run: () => open("mode") },
  ];
  const commands: AppCommand[] = [
    ...repositoryCommands(props.session),
    { id: "settings.open", title: "Open settings", category: "Settings", run: () => open("settings") },
    ...settingsCommands,
    { id: "status.error", title: "Show errors and warnings", category: "Status", run: () => open("error") },
    { id: "status.clear", title: "Dismiss last operation error", category: "Status", run: () => props.session.clearError() },
    { id: "app.quit", title: "Quit", category: "App", keys: ["q"], run: props.quit },
  ];
  const items = createMemo<AppCommand[]>(() => {
    let options = commands;
    if (dialog() === "settings") options = settingsCommands;
    if (dialog() === "theme") options = props.themes.names.map(name => ({ id: name, title: name, category: "Settings › Theme", run: () => updateSettings({ theme: name }) }));
    if (dialog() === "mode") options = (["system", "light", "dark"] as const).map(mode => ({ id: mode, title: mode, category: "Settings › Color mode", run: () => updateSettings({ colorMode: mode }) }));
    if (!query().trim()) return options;
    return fuzzysort.go(query(), options.map(option => ({ ...option, search: `${option.title} ${option.category ?? ""} ${option.keywords ?? ""}` })), { key: "search", threshold: 0.25 }).map(result => result.obj);
  });
  const run = (command: AppCommand) => {
    const reason = command.unavailable?.();
    if (reason) { props.session.reportError(reason); return; }
    close();
    void Promise.resolve(command.run()).catch(error => props.session.reportError(error));
  };
  const activate = () => {
    if (dialog() === "error") return;
    if (dialog() === "maxCommits") {
      const maxCommits = Number(query());
      if (!/^\d+$/.test(query()) || !Number.isSafeInteger(maxCommits) || maxCommits < 1) {
        props.session.reportError("Maximum commits must be a positive whole number.");
        return;
      }
      close();
      void updateSettings({ maxCommits });
      return;
    }
    if (dialog() === "interval") {
      const minutes = Number(query());
      if (!/^\d+$/.test(query()) || !Number.isInteger(minutes) || minutes < 1 || minutes > 1440) {
        props.session.reportError("Fetch interval must be a whole number from 1 to 1440 minutes.");
        return;
      }
      close();
      void updateSettings({ fetchIntervalMinutes: minutes });
      return;
    }
    const command = items()[choice()];
    if (command) run(command);
  };
  const navigate = (delta: number) => {
    if (pane() === "details") detailBox?.scrollBy(delta);
    else void props.session.move(delta);
  };
  const modalMove = (delta: number) => {
    if (dialog() === "error") { errorBox?.scrollBy(delta); return; }
    setChoice(index => Math.max(0, Math.min(items().length - 1, index + delta)));
  };
  const keymap = createDefaultOpenTuiKeymap(renderer);
  const registrations = [
    keymap.registerLayer({
      commands: commands.map(command => ({ name: command.id, title: command.title, run: () => run(command) })),
      bindings: [
        { key: "ctrl+p", cmd: () => dialog() ? close() : open("commands") },
        { key: "ctrl+c", cmd: () => { if (dialog()) close(); else void props.quit(); } },
      ],
    }),
    keymap.registerLayer({
      enabled: () => !dialog(),
      bindings: [
        ...commands.flatMap(command => (command.keys ?? []).map(key => ({ key, cmd: command.id }))),
        ...["down", "j"].map(key => ({ key, cmd: () => navigate(1) })),
        ...["up", "k"].map(key => ({ key, cmd: () => navigate(-1) })),
        { key: "pagedown", cmd: () => navigate(visibleCount()) },
        { key: "pageup", cmd: () => navigate(-visibleCount()) },
        ...["tab", "shift+tab"].map(key => ({ key, cmd: () => setPane(pane() === "history" ? "details" : "history") })),
        { key: "return", cmd: () => { if (narrow()) setPane("details"); } },
        { key: "escape", cmd: () => setPane("history") },
      ],
    }),
    keymap.registerLayer({
      priority: 10, enabled: () => !!dialog(),
      bindings: [
        { key: "escape", cmd: close },
        { key: "return", cmd: activate },
        { key: "up", cmd: () => modalMove(-1) },
        { key: "down", cmd: () => modalMove(1) },
        { key: "pageup", cmd: () => modalMove(-8) },
        { key: "pagedown", cmd: () => modalMove(8) },
        { key: "tab", cmd: () => modalMove(1) },
        { key: "shift+tab", cmd: () => modalMove(-1) },
      ],
    }),
  ];

  onMount(() => {
    const unsubscribe = props.session.subscribe(setState);
    const modeChanged = (mode: "light" | "dark") => setSystemMode(mode);
    renderer.on(CliRenderEvents.THEME_MODE, modeChanged);
    historyBox?.focus();
    onCleanup(() => { unsubscribe(); renderer.off(CliRenderEvents.THEME_MODE, modeChanged); });
  });
  onCleanup(() => registrations.reverse().forEach(unregister => unregister()));
  createEffect(() => {
    const rows = state().rows;
    const count = visibleCount();
    let nextTop = top();
    if (rows !== previousRows) {
      const anchor = previousRows[nextTop]?.sha;
      const index = rows.findIndex(row => row.sha === anchor);
      if (index >= 0) nextTop = index;
      previousRows = rows;
    }
    const index = rows.findIndex(row => row.sha === state().selected);
    if (index >= 0 && index < nextTop) nextTop = index;
    if (index >= nextTop + count) nextTop = index - count + 1;
    setTop(Math.max(0, Math.min(nextTop, rows.length - count)));
  });
  const selected = createMemo(() => state().selected);
  createEffect(() => { selected(); detailBox?.scrollTo(0); });
  const visibleRows = () => state().rows.slice(top(), top() + visibleCount());
  const outside = () => state().selected && !state().rows.some(row => row.sha === state().selected);
  const currentValue = (id: string) => ({
    "settings.fetch": preferences().autoFetch ? "On" : "Off",
    "settings.interval": `${preferences().fetchIntervalMinutes} minutes`,
    "settings.maxCommits": String(preferences().maxCommits),
    "settings.theme": preferences().theme,
    "settings.mode": preferences().colorMode,
  }[id] ?? "");
  const numericDialog = () => dialog() === "interval" || dialog() === "maxCommits";
  const modalTop = () => Math.max(1, Math.floor(dimensions().height / 6));
  const modalWidth = () => Math.max(1, Math.min(84, dimensions().width - 2));
  const modalCount = () => Math.max(1, Math.min(12, dimensions().height - modalTop() - 8));
  const modalStart = () => Math.max(0, choice() - modalCount() + 1);
  const modalTitle = () => ({ commands: "Commands", settings: "Settings", theme: "Settings · theme", mode: "Settings · color mode", interval: "Settings · fetch interval", maxCommits: "Settings · maximum commits", error: "Errors and warnings" }[dialog() ?? "commands"]);
  const suffix = (item: AppCommand) => [currentValue(item.id) || item.keys?.join(", "), item.category].filter(Boolean).join(" · ");

  return <box width="100%" height="100%" padding={1} flexDirection="column" backgroundColor={theme().background.base}>
    <box height={2} flexDirection="column" flexShrink={0} paddingLeft={1} paddingRight={1}>
      <text fg={theme().text.base} height={1} wrapMode="none" truncate>{`gitbuddy · ${line(basename(props.session.repository.root))} · ${line(state().status?.branch ?? (state().status?.head ? "detached HEAD" : "No branch"))}`}</text>
      <text fg={theme().text.muted} height={1} wrapMode="none" truncate>{`${state().scope === "all" ? "All branches and tags" : "Current branch"}${state().status?.upstream ? ` · tracks ${line(state().status!.upstream!)}` : " · no upstream"}${state().status?.dirty ? " · working tree changed" : ""}`}</text>
    </box>
    <box flexDirection="row" flexGrow={1} minHeight={0}>
      <Show when={!narrow() || pane() === "history"}>
        <box ref={box => { historyBox = box; box.focusable = true; }} focused={!dialog() && pane() === "history"}
          width={narrow() ? "100%" : "56%"} flexDirection="column" padding={1} backgroundColor={panelBackground("history")}>
          <text height={1} flexShrink={0} marginBottom={1} fg={theme().text.muted} attributes={pane() === "history" ? TextAttributes.BOLD : TextAttributes.NONE}>
            History
          </text>
          <Show when={state().rows.length} fallback={<text fg={theme().text.muted}>{state().loading ? "" : "No commits in this view."}</text>}>
            <For each={visibleRows()}>{row => <box height={2} flexShrink={0} flexDirection="column" overflow="hidden" paddingLeft={1}
              border={["left"]} customBorderChars={selectionEdge}
              borderColor={row.sha === state().selected ? accent() : panelBackground("history")}
              backgroundColor={row.sha === state().selected ? theme().background.raised.high : panelBackground("history")}>
              <text fg={theme().text.base} height={1} wrapMode="none" truncate>{line(row.subject)}</text>
              <text fg={theme().text.muted} height={1} wrapMode="none" truncate>{`${row.sha.slice(0, 7)} · ${line(row.author)} · ${row.authoredAt.slice(0, 10)}${row.labels.length ? ` · ${row.labels.map(line).join(" ")}` : ""}`}</text>
            </box>}</For>
          </Show>
        </box>
      </Show>
      <Show when={!narrow() || pane() === "details"}>
        <box width={narrow() ? "100%" : "44%"} padding={1} flexDirection="column" backgroundColor={panelBackground("details")}>
          <text height={1} flexShrink={0} fg={theme().text.muted} attributes={pane() === "details" ? TextAttributes.BOLD : TextAttributes.NONE}>Commit details</text>
          <scrollbox ref={detailBox} focused={!dialog() && pane() === "details"} flexGrow={1} minHeight={0}
            scrollX={false} contentOptions={{ flexDirection: "column" }}>
          <Show when={outside()}><text fg={theme().text.feedback.warning.base}>Selected commit is outside the loaded history. Navigate to select another.</text></Show>
          <Show when={state().details} fallback={
            <Show when={detailsActivity()} fallback={<text fg={theme().text.muted}>{state().detailsLoading ? "" : "Select a commit."}</text>}>
              <Spinner label="Loading commit..." color={accent()} textColor={theme().text.muted} />
            </Show>
          }>{(details: () => CommitDetails) => <>
            <text fg={theme().text.base}>{terminalSafe(details().message)}</text>
            <text fg={theme().text.muted}>{`\n${details().sha}\nAuthor: ${line(details().author)} <${line(details().authorEmail)}>\n${line(details().authoredAt)}\nCommitter: ${line(details().committer)} <${line(details().committerEmail)}>\n${line(details().committedAt)}\nParents: ${details().parents.join(" ") || "none (root)"}\n\n${line(details().comparison)}\n`}</text>
            <For each={details().files}>{file => <text fg={theme().text.base}>{`${file.status}  ${file.oldPath ? `${line(file.oldPath)} → ` : ""}${line(file.path)}`}</text>}</For>
          </>}</Show>
          </scrollbox>
        </box>
      </Show>
    </box>
    <box height={footerHeight()} flexShrink={0} flexDirection="column">
      <box height={1} flexShrink={0} paddingLeft={1} paddingRight={1}>
        <Show when={activity()} fallback={<text height={1} wrapMode="none" truncate fg={theme().text.muted}>{state().closing ? "Quitting..." : `${state().status?.remotes.length ? state().lastFetch ? `Fetched ${new Date(state().lastFetch!).toLocaleTimeString()}` : "Not fetched this session" : "No remotes"}${state().fetchFailed ? " · last fetch FAILED (view may be stale)" : ""}`}</text>}>
          <Spinner label={`${activity()}${state().closing ? " · waiting to quit" : ""}`} color={accent()} textColor={theme().text.muted} />
        </Show>
      </box>
      <Show when={notices()}><box height={1} flexShrink={0} paddingLeft={1} paddingRight={1}>
        <text height={1} wrapMode="none" truncate fg={theme().text.feedback.warning.base}>{`! ${line(notices())} — Ctrl+P: Show errors and warnings`}</text>
      </box></Show>
      <box height={1} flexShrink={0} paddingLeft={1} paddingRight={1} backgroundColor={theme().background.raised.base}>
        <text height={1} wrapMode="none" truncate fg={theme().text.muted}>↑↓/jk Navigate · Tab Pane · Enter Details · r Refresh · Ctrl+P Commands · q Quit</text>
      </box>
    </box>
    <Show when={dialog()}>
      <box position="absolute" zIndex={10} left={0} top={0} width={dimensions().width} height={dimensions().height} backgroundColor={backdrop()}>
      <box position="absolute" left={Math.max(1, Math.floor((dimensions().width - modalWidth()) / 2))}
        top={modalTop()} width={modalWidth()}
        maxHeight={Math.max(5, dimensions().height - modalTop() - 1)} flexDirection="column" padding={1}
        backgroundColor={theme().background.raised.base}>
        <text height={1} flexShrink={0} fg={theme().text.base} attributes={TextAttributes.BOLD}>{modalTitle()}</text>
        <Show when={dialog() !== "error"} fallback={
          <scrollbox ref={errorBox} focused height={Math.max(3, dimensions().height - modalTop() - 7)} scrollX={false}>
            <text fg={theme().text.base}>{terminalSafe(notices() || "No errors or warnings.")}</text>
          </scrollbox>
        }>
          <input id="palette-input" focused value={query()} maxLength={256} flexShrink={0} marginBottom={1}
            placeholder={dialog() === "interval" ? "Interval in minutes (1–1440)" : dialog() === "maxCommits" ? "Maximum commits (positive whole number)" : "Search commands and settings…"}
            onInput={value => { setQuery(value); setChoice(0); }}
            backgroundColor={theme().background.raised.base} textColor={theme().text.base} />
          <Show when={!numericDialog()} fallback={<text fg={theme().text.muted}>{dialog() === "interval" ? "Whole minutes, 1–1440. Enter saves; Esc cancels." : "Positive whole number. Enter saves; Esc cancels."}</text>}>
            <For each={items().slice(modalStart(), modalStart() + modalCount())}>{(item, index) => <box height={1} flexShrink={0} width="100%"
              flexDirection="row" paddingLeft={1} paddingRight={1} overflow="hidden" border={["left"]} customBorderChars={selectionEdge}
              borderColor={choice() === index() + modalStart() ? accent() : theme().background.raised.base}
              backgroundColor={choice() === index() + modalStart() ? theme().background.raised.high : theme().background.raised.base}>
              <text height={1} flexGrow={1} flexShrink={1} minWidth={0} wrapMode="none" truncate
                fg={item.unavailable?.() ? theme().text.muted : theme().text.base}>{line(item.title)}</text>
              <text height={1} maxWidth="45%" flexShrink={0} marginLeft={1} wrapMode="none" truncate textAlign="right" fg={theme().text.muted}>{line(suffix(item))}</text>
            </box>}</For>
            <Show when={!items().length}><text fg={theme().text.muted}>No matching commands or settings.</text></Show>
          </Show>
        </Show>
        <text height={1} flexShrink={0} marginTop={1} wrapMode="none" truncate fg={theme().text.muted}>
          {(!numericDialog() && dialog() !== "error" && items()[choice()]?.unavailable?.()) || "↑↓ Choose · Enter Apply · Esc Back"}
        </text>
      </box>
      </box>
    </Show>
  </box>;
}
