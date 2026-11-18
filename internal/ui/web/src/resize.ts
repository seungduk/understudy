// Resizable panes: a drag handle between two panes sets the width of one of them. Each width is a
// CSS variable on the root (--w-<pane>), so it survives re-renders, and is remembered per viewer.

import { h } from "./dom";

// The panes that can be resized. "start" panes sit before their handle, "end" panes after it.
export type Pane = "nav" | "detail" | "outline" | "problems";

const PANES: Record<Pane, { side: "start" | "end"; min: number; max: number; label: string }> = {
  nav: { side: "start", min: 120, max: 360, label: "views" },
  detail: { side: "end", min: 320, max: 1200, label: "request detail" },
  outline: { side: "start", min: 140, max: 480, label: "outline" },
  problems: { side: "end", min: 240, max: 720, label: "problems" },
};

const storeKey = (p: Pane) => "understudy-w-" + p;

function load(p: Pane): number | null {
  try {
    const n = Number(localStorage.getItem(storeKey(p)));
    return Number.isInteger(n) && n > 0 ? n : null;
  } catch {
    return null; // storage may be off: widths just are not remembered
  }
}

function save(p: Pane, w: number | null): void {
  try {
    if (w === null) localStorage.removeItem(storeKey(p));
    else localStorage.setItem(storeKey(p), String(w));
  } catch {
    /* storage may be off */
  }
}

function apply(p: Pane, w: number | null): void {
  const { min, max } = PANES[p];
  if (w === null) document.documentElement.style.removeProperty("--w-" + p);
  else document.documentElement.style.setProperty("--w-" + p, Math.max(min, Math.min(max, w)) + "px");
}

// restoreWidths applies the widths this viewer chose before.
export function restoreWidths(): void {
  for (const p of Object.keys(PANES) as Pane[]) apply(p, load(p));
}

// handle is the drag handle for a pane: drag it, or focus it and press the arrow keys;
// a double click goes back to the default width.
export function handle(p: Pane): HTMLElement {
  const { side, min, max, label } = PANES[p];
  const el = h("div.handle", { role: "separator", "aria-orientation": "vertical", "aria-label": "resize " + label, tabindex: "0", title: "Drag to resize; double-click to reset" });
  const pane = () => (side === "start" ? el.previousElementSibling : el.nextElementSibling) as HTMLElement | null;
  const set = (w: number) => {
    const c = Math.round(Math.max(min, Math.min(max, w)));
    apply(p, c);
    save(p, c);
  };
  el.addEventListener("pointerdown", (ev) => {
    const target = pane();
    if (!target) return;
    ev.preventDefault();
    const x0 = ev.clientX;
    const w0 = target.getBoundingClientRect().width;
    el.setPointerCapture(ev.pointerId);
    document.documentElement.classList.add("resizing");
    const move = (m: PointerEvent) => set(w0 + (side === "start" ? m.clientX - x0 : x0 - m.clientX));
    const up = () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", up);
      document.documentElement.classList.remove("resizing");
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", up);
  });
  el.addEventListener("keydown", (ev) => {
    const target = pane();
    if (!target || (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight")) return;
    ev.preventDefault();
    ev.stopPropagation();
    const grow = (ev.key === "ArrowRight") === (side === "start");
    set(target.getBoundingClientRect().width + (grow ? 16 : -16));
  });
  el.addEventListener("dblclick", () => {
    apply(p, null);
    save(p, null);
  });
  return el;
}
