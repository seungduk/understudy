// Renderers shared by the views: state pills, answers, tool results, divergences.
import type { Answer, Entry, Result, Session, State } from "./api";
import { h, plural, time, type Child } from "./dom";
import { json } from "./highlight";

export const STATE_HELP: Record<State, string> = {
  idle: "the script has this session; no request yet",
  waiting: "the client owes the next request (tool results, or a retry)",
  streaming: "a reply is being sent",
  stalled: "a reply is held by a stall",
  diverged: "the last request did not fit the script",
  done: "the client got a final answer or a limit",
};

export function pill(state: State): HTMLElement {
  return h(`span.pill.s-${state}`, { title: STATE_HELP[state] }, h("span.dot", { "aria-hidden": "true" }), state);
}

export function outcomeBadge(e: Entry): HTMLElement {
  const bad = e.outcome === "diverged" || e.outcome === "no_script" || e.outcome === "bad_request";
  const warn = e.outcome === "cut_close" || e.outcome === "cut_reset" || e.outcome === "client_gone" || (e.status ?? 0) >= 400;
  const cls = bad ? "bad" : e.outcome === "pending" ? (e.held ? "hold" : "live") : warn ? "warn" : e.divergence ? "warn" : "ok";
  return h(`span.badge.o-${cls}`, e.held ? "stalled" : e.outcome.replace("_", " "));
}

export function stepLabel(e: { step?: string; branch?: string; attempt?: number }): Child {
  if (!e.step) return h("span.muted", "—");
  return [
    h("span.step", e.step),
    e.branch && e.branch !== "reply" ? h("span.branch", e.branch) : null,
    e.attempt && e.attempt > 1 ? h("span.attempt", `try ${e.attempt}`) : null,
  ];
}

// collapsible keeps long text short until asked.
function block(text: string, cls = "text"): HTMLElement {
  const long = text.length > 1200 || text.split("\n").length > 18;
  const pre = h(`pre.${cls}`, text);
  if (!long) return pre;
  pre.classList.add("clamped");
  const more = h("button.link", { type: "button" }, "show all");
  more.addEventListener("click", () => {
    pre.classList.toggle("clamped");
    more.textContent = pre.classList.contains("clamped") ? "show all" : "show less";
  });
  return h("div", pre, more);
}

// promptBlocks splits Claude Code's prompt into its system reminders (folded) and the rest.
export function prompt(text: string): HTMLElement {
  const parts: Child[] = [];
  const re = /<system-reminder>([\s\S]*?)<\/system-reminder>/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) {
    const before = text.slice(last, m.index).trim();
    if (before) parts.push(block(before));
    parts.push(h("details.reminder", h("summary", "system reminder"), h("pre.text", m[1].trim())));
    last = re.lastIndex;
  }
  const rest = text.slice(last).trim();
  if (rest) parts.push(block(rest));
  return h("div.prompt", parts);
}

export function toolResult(r: Result, i: number): HTMLElement {
  return h("div.card.result" + (r.is_error ? ".is-error" : ""),
    h("div.card-h",
      h("span.kind", "tool_result"),
      h("span.tool", r.tool || "unknown tool"),
      h(`span.badge.${r.is_error ? "o-bad" : "o-ok"}`, r.is_error ? "error" : "ok"),
      h("span.muted.right", `#${i}`)),
    block(r.text || "(empty)", "out"));
}

function callSummary(input: unknown): string {
  if (!input || typeof input !== "object") return "";
  const o = input as Record<string, unknown>;
  for (const k of ["command", "file_path", "pattern", "path", "url", "description"]) {
    if (typeof o[k] === "string") return o[k] as string;
  }
  return "";
}

function resetsIn(s: number): string {
  if (s >= 3600) return `${Math.round(s / 360) / 10} h`;
  if (s >= 60) return `${Math.round(s / 6) / 10} min`;
  return `${s} s`;
}

export function answer(a: Answer, status?: number): HTMLElement {
  const parts: Child[] = [];
  if (a.thinking) parts.push(h("details.thinking", { open: true }, h("summary", "thinking"), h("pre.text", a.thinking)));
  switch (a.kind) {
    case "message":
    case "tool_use":
      if (a.text) parts.push(block(a.text));
      for (const c of a.calls || []) {
        parts.push(h("div.card.call",
          h("div.card-h", h("span.kind", "tool_use"), h("span.tool", c.tool), h("span.summary", callSummary(c.input))),
          json(c.input)));
      }
      if (a.kind === "message" && a.stop === "max_tokens") parts.push(h("div.note", "stopped: max_tokens"));
      break;
    case "http_error":
      parts.push(h("div.card.err",
        h("div.card-h", h("span.kind", "http_error"), h("span.tool", `${status ?? ""} ${a.error}`),
          a.retry && a.retry !== "default" ? h("span.muted", `x-should-retry: ${a.retry === "never" ? "false" : "true"}`) : null),
        a.message ? h("pre.text", a.message) : null));
      break;
    case "stream_error":
      if (a.text) parts.push(block(a.text));
      parts.push(h("div.card.err", h("div.card-h", h("span.kind", "stream_error"), h("span.tool", a.error || "")),
        a.message ? h("pre.text", a.message) : null));
      break;
    case "limit":
      parts.push(h("div.card.limit",
        h("div.card-h", h("span.kind", "limit"), h("span.tool", (a.window || "").replace(/_/g, " ")), h("span.muted", `429 · resets in ${resetsIn(a.resets_in_seconds || 0)}`)),
        h("pre.text", a.window === "five_hour" ? "You've hit your 5-hour limit" : a.window?.includes("opus") ? "You've hit your Opus limit" : "You've hit your weekly limit")));
      break;
  }
  if (a.faults?.length) parts.push(h("div.faults", a.faults.map((f) => h("span.fault", f))));
  return h("div.answer", parts);
}

export function divergence(e: Entry): HTMLElement {
  const strict = e.outcome === "diverged" || e.outcome === "no_script";
  return h("div.diverge" + (strict ? "" : ".lenient"), { role: "alert" },
    h("div.diverge-h", h("strong", strict ? "Diverged" : "Diverged (lenient: answered anyway)"), e.step ? h("span", " at step ", h("span.step", e.step)) : null),
    h("p.diverge-msg", e.divergence || e.error || ""),
    e.expected?.length || e.actual?.length ? h("div.eva",
      h("div", h("div.eva-h", "Expected (script)"), h("ul", (e.expected || []).map((x) => h("li", x)))),
      h("div", h("div.eva-h", "Actual (request)"), h("ul", (e.actual || []).map((x) => h("li", x))))) : null,
    e.trace?.length ? h("details", h("summary", "why: every test"), h("ul.trace", e.trace.map((x) => h("li", x)))) : null);
}

export function held(e: Entry, release: (g: string) => void): HTMLElement | null {
  if (!e.held) return null;
  const g = e.held.gate;
  return h("div.held",
    h("span.pill.s-stalled", h("span.dot"), "stalled"),
    g ? h("span", " waiting on gate ", h("code", g), ` since ${time(e.held.since)}`) : h("span", ` for ${e.held.seconds} s since ${time(e.held.since)}`),
    g ? h("button.btn.primary", { type: "button", onclick: () => release(g), "data-release": g }, `Release ${g}`) : null);
}

export function sessionLine(s: Session): string {
  return `${plural(s.requests, "request")}${s.divergences ? `, ${plural(s.divergences, "divergence")}` : ""}`;
}
