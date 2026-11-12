import { Renderable, RGBA, type OptimizedBuffer, type RenderContext, type RenderableOptions } from "@opentui/core";
import { extend } from "@opentui/solid";

// Adapted from OpenCode v2.2.7; provenance and MIT notice: THIRD_PARTY_NOTICES.md.
const frames = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
const transparent = RGBA.fromValues(0, 0, 0, 0);

interface SpinnerOptions extends RenderableOptions {
  color: RGBA;
}

class SpinnerRenderable extends Renderable {
  private index = 0;
  private _color: RGBA;

  constructor(ctx: RenderContext, options: SpinnerOptions) {
    super(ctx, options);
    this._color = options.color;
    this.setMeasureProvider(() => ({ width: 1, height: 1 }));
    // The renderer owns the shared clock and visibility/disposal cleanup. Do not
    // set live: it would prevent renderer.idle() from settling.
    this.animationFps = 1000 / 80;
  }

  get color() { return this._color; }
  set color(value: RGBA) {
    this._color = value;
    this.requestRender();
  }

  protected override onAnimationTick(_delta: number, ticks: number) {
    this.index = (this.index + ticks) % frames.length;
    this.requestRender();
  }

  protected override renderSelf(buffer: OptimizedBuffer) {
    buffer.drawText(frames[this.index]!, this.screenX, this.screenY, this._color, transparent);
  }
}

extend({ "opencode-spinner": SpinnerRenderable });
declare module "@opentui/solid" {
  interface OpenTUIComponents {
    "opencode-spinner": typeof SpinnerRenderable;
  }
}

/** Mount only while busy; the action label remains readable without animation. */
export function Spinner(props: { label: string; color: RGBA; textColor?: RGBA }) {
  return <box flexDirection="row" height={1} minWidth={1} flexShrink={1} gap={1} overflow="hidden">
    <opencode-spinner color={props.color} width={1} minWidth={1} height={1} flexShrink={0} />
    <text fg={props.textColor ?? props.color} flexShrink={1} minWidth={0} height={1} wrapMode="none" truncate>{props.label}</text>
  </box>;
}
