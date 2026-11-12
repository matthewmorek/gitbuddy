import { expect, test } from "bun:test";
import { RGBA } from "@opentui/core";
import { ManualClock } from "@opentui/core/testing";
import { testRender } from "@opentui/solid";
import { createSignal, Show } from "solid-js";
import { Spinner } from "../src/spinner";

test("spinner renders the action in its theme color, advances on the renderer clock, and retires when settled", async () => {
  const clock = new ManualClock();
  const accent = RGBA.fromHex("#abcdef");
  const changedAccent = RGBA.fromHex("#fedcba");
  const [color, setColor] = createSignal(accent);
  const [busy, setBusy] = createSignal(true);
  const ui = await testRender(() => <Show when={busy()}><Spinner label="Fetching..." color={color()} /></Show>, { width: 24, height: 2, clock });
  try {
    await ui.renderOnce();
    expect(ui.captureCharFrame().split("\n")[0]!.trimEnd()).toBe("⠋ Fetching...");
    expect(ui.captureSpans().lines[0]!.spans[0]!.fg.equals(accent)).toBe(true);
    // Animation must not hold the renderer live while it waits for its next tick.
    await ui.renderer.idle();
    clock.advance(80);
    await ui.renderOnce();
    expect(ui.captureCharFrame()).toContain("⠙ Fetching...");
    setColor(changedAccent);
    await ui.renderOnce();
    expect(ui.captureSpans().lines[0]!.spans[0]!.fg.equals(changedAccent)).toBe(true);
    ui.resize(8, 2);
    await ui.renderOnce();
    const narrow = ui.captureCharFrame().split("\n");
    expect(narrow[0]).toMatch(/^⠙ F/);
    expect(narrow[0]!.length).toBe(8);
    expect(narrow[1]!.trim()).toBe("");
    setBusy(false);
    await ui.renderOnce();
    // Solid defers recursive destruction to nextTick; cleanup during this frame
    // can queue another repaint. Advance AFTER rendering so idle can drain it.
    clock.advance(80);
    await ui.renderer.idle();
    expect(ui.captureCharFrame().trim()).toBe("");
    const settledFrame = ui.renderer.frameId;
    clock.advance(800);
    await ui.renderer.idle();
    expect(ui.renderer.frameId).toBe(settledFrame);
  } finally {
    await ui.dispose();
  }
  expect(clock.pendingTimerCount).toBe(0);
});
