// understudy's web UI: a thin layer over the control API at /_understudy/.
import { api, BASE, OUTCOMES, type Clock, type Entry, type Gate, type Health, type Problem, type ScriptInfo, type Session } from "./api";
import { h, plural, replace, time, type Child } from "./dom";
import { yaml } from "./highlight";
import { answer, divergence, held, outcomeBadge, pill, prompt, sessionLine, stepLabel, toolResult, STATE_HELP } from "./render";

// ---- state -----------------------------------------------------------------

type Live = "connecting" | "live" | "offline";
const S = {
  health: null as Health | null,
  clock: null as Clock | null,
  requests: [] as Entry[],
  sessions: [] as Session[],
  gates: [] as Gate[],
  info: { loaded: false } as ScriptInfo,
  source: "",
  live: "connecting" as Live,
  error: "",
};

let inflight = false;
let again = false;
async function refresh(): Promise<void> {
  if (inflight) {
    again = true;
    return;
  }
  inflight = true;
  try {
    const [health, clock, requests, sessions, gates, info] = await Promise.all([
      api.health(), api.clock(), api.requests(), api.sessions(), api.gates(), api.scriptInfo()]);
    const scriptChanged = JSON.stringify(info) !== JSON.stringify(S.info) || (info.loaded && !S.source);
    Object.assign(S, { health, clock, requests, sessions, gates, info, error: "" });
    if (scriptChanged) S.source = info.loaded ? await api.script() : "";
  } catch (e) {
    S.error = (e as Error).message;
  } finally {
    inflight = false;
  }
  render();
  if (again) {
    again = false;
    void refresh();
  }
}

function connect(): void {
  const es = new EventSource(BASE + "events");
  es.addEventListener("change", () => {
    S.live = "live";
    void refresh();
  });
  es.onerror = () => {
    S.live = es.readyState === EventSource.CLOSED ? "offline" : "connecting";
    renderHeader();
    if (es.readyState === EventSource.CLOSED) setTimeout(connect, 2000);
  };
}

// ---- routing -----------------------------------------------------------------

type View = "sessions" | "requests" | "gates" | "script";
interface Route { view: View; key?: string; seq?: number; q: URLSearchParams }

function route(): Route {
  const [path, query] = location.hash.replace(/^#\/?/, "").split("?");
  const [view, ...rest] = path.split("/");
  const q = new URLSearchParams(query || "");
  const arg = rest.length ? decodeURIComponent(rest.join("/")) : undefined;
  switch (view) {
    case "requests":
      return { view, seq: arg ? Number(arg) : undefined, q };
    case "gates":
    case "script":
      return { view, q };
  }
  return { view: "sessions", key: arg, q };
}

function go(hash: string): void {
  if (location.hash !== hash) location.hash = hash;
}

const sessionHash = (k: string) => "#/sessions/" + encodeURIComponent(k);
const requestHash = (seq: number) => "#/requests/" + seq + (location.hash.includes("?") ? "?" + location.hash.split("?")[1] : "");

// ---- theme -------------------------------------------------------------------

function store(k: string, v?: string): string | null {
  try {
    if (v === undefined) return localStorage.getItem(k);
    localStorage.setItem(k, v);
  } catch {
    /* storage may be off: the theme just is not remembered */
  }
  return null;
}

function applyTheme(t: string | null): void {
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
  else delete document.documentElement.dataset.theme;
}

function toggleTheme(): void {
  const dark = document.documentElement.dataset.theme
    ? document.documentElement.dataset.theme === "dark"
    : matchMedia("(prefers-color-scheme: dark)").matches;
  const t = dark ? "light" : "dark";
  store("understudy-theme", t);
  applyTheme(t);
}

// ---- actions -----------------------------------------------------------------

let toastTimer = 0;
function toast(msg: string, bad = false): void {
  const el = document.getElementById("toast")!;
  replace(el, msg);
  el.className = "toast show" + (bad ? " bad" : "");
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => (el.className = "toast"), 3500);
}

async function release(gate: string): Promise<void> {
  try {
    const r = await api.release(gate);
    toast(`Released ${gate}: ${plural(r.waiting, "request")} went on`);
  } catch (e) {
    toast((e as Error).message, true);
  }
}

let resetArmed = 0;
async function reset(btn: HTMLElement): Promise<void> {
  if (!resetArmed) {
    btn.textContent = "Confirm reset";
    btn.classList.add("danger");
    resetArmed = window.setTimeout(() => {
      resetArmed = 0;
      btn.textContent = "Reset";
      btn.classList.remove("danger");
    }, 3000);
    return;
  }
  clearTimeout(resetArmed);
  resetArmed = 0;
  await api.reset();
  toast("The log, retry counts and gates are cleared; the script stays");
}

// ---- shell -------------------------------------------------------------------

const app = document.getElementById("app")!;
const header = h("header.top");
const nav = h("nav.tabs", { "aria-label": "views" });
const main = h("main.main", { id: "main", tabindex: "-1" });
app.append(header, h("div.body", nav, main), h("div.toast", { id: "toast", role: "status", "aria-live": "polite" }));

function renderHeader(): void {
  const hl = S.health;
  const steps = (S.info.sessions || []).reduce((n, s) => n + s.steps.length, 0);
  const resetBtn = h("button.btn", { type: "button", title: "Forget the log, retry counts and gates" }, "Reset");
  resetBtn.addEventListener("click", () => void reset(resetBtn));
  replace(header,
    h("a.brand", { href: "#/sessions", "aria-label": "understudy home" },
      h("img", { src: "logo.svg", alt: "", width: 22, height: 22 }), h("span", "understudy"),
      hl ? h("span.ver", hl.version) : null),
    h("div.chips",
      S.info.loaded
        ? h("a.chip.ok", { href: "#/script", title: "the loaded script checks" }, "script ", h("b", plural(S.info.sessions?.length || 0, "session")), ` · ${plural(steps, "step")}`)
        : h("a.chip.warn", { href: "#/script" }, "no script loaded"),
      S.clock ? h("span.chip", { title: "the server's clock" }, S.clock.mode === "virtual" ? `virtual ${time(S.clock.now)}` : "real clock") : null,
      h(`span.chip.live.${S.live}`, { title: "live updates from " + BASE + "events" }, h("span.dot"), S.live)),
    h("div.actions",
      resetBtn,
      h("button.btn.icon", { type: "button", title: "Light or dark (t)", "aria-label": "toggle theme", onclick: toggleTheme }, "◐"),
      h("button.btn.icon", { type: "button", title: "Keyboard shortcuts (?)", "aria-label": "keyboard shortcuts", onclick: () => help(true) }, "?")));
}

function renderNav(r: Route): void {
  const waiting = S.gates.reduce((n, g) => n + g.waiting, 0);
  const msgs = S.requests.filter((e) => e.outcome !== "other").length;
  const diverged = S.sessions.filter((s) => s.state === "diverged").length;
  const tab = (v: View, label: string, key: string, count: Child, hot = false) =>
    h("a.tab" + (r.view === v ? ".on" : ""), { href: "#/" + v, "aria-current": r.view === v ? "page" : null, title: `${label} (${key})` },
      h("span", label), count !== null ? h("span.count" + (hot ? ".hot" : ""), count) : null);
  replace(nav,
    tab("sessions", "Sessions", "1", S.sessions.length, diverged > 0),
    tab("requests", "Requests", "2", msgs),
    tab("gates", "Gates", "3", waiting ? `${waiting} waiting` : S.gates.length, waiting > 0),
    tab("script", "Script", "4", S.info.loaded ? "✓" : "—"));
}

// ---- views -------------------------------------------------------------------

let current: { view: View; key?: string; seq?: number } | null = null;
let selected = 0; // the selected row of the list view

function render(): void {
  renderHeader();
  const r = route();
  renderNav(r);
  const same = current && current.view === r.view && current.key === r.key && current.seq === r.seq;
  if (!same) selected = 0;
  current = { view: r.view, key: r.key, seq: r.seq };
  const y = main.scrollTop;
  main.classList.toggle("fill", r.view === "requests");
  switch (r.view) {
    case "sessions":
      r.key !== undefined ? sessionView(r.key) : sessionsView();
      break;
    case "requests":
      requestsView(r);
      break;
    case "gates":
      gatesView();
      break;
    case "script":
      scriptView();
      break;
  }
  if (same) main.scrollTop = y;
  if (S.error) main.prepend(h("div.banner.bad", `Cannot reach the server: ${S.error}`));
}

function empty(title: string, ...body: Child[]): HTMLElement {
  return h("div.empty", h("h2", title), body);
}

const noRequests = () => empty("No requests yet",
  h("p", "Point Claude Code at this server, with dummy credentials only:"),
  h("pre.cmd", `eval "$(understudy env --url ${location.origin})"\nclaude -p --output-format stream-json --verbose "say hi" </dev/null`));

function rows(list: HTMLElement[]): void {
  selected = Math.max(0, Math.min(selected, list.length - 1));
  list.forEach((tr, i) => {
    tr.classList.toggle("sel", i === selected);
    tr.setAttribute("aria-selected", String(i === selected));
  });
}

function sessionsView(): void {
  if (!S.sessions.length) {
    replace(main, S.info.loaded ? noRequests() : empty("No script loaded",
      h("p", "Start the server with a script, or load one on the Script tab:"),
      h("pre.cmd", "understudy serve --script hello.yaml")));
    return;
  }
  const order = { stalled: 0, diverged: 1, streaming: 2, waiting: 3, done: 4, idle: 5 };
  const list = [...S.sessions].sort((a, b) => order[a.state] - order[b.state] || (b.last_seq || 0) - (a.last_seq || 0) || a.key.localeCompare(b.key));
  const trs = list.map((s) =>
    h("tr" + (s.state === "idle" ? ".dim" : "") + (s.state === "diverged" ? ".row-bad" : ""), { "data-href": sessionHash(s.key), tabindex: "-1" },
      h("td.c-state", pill(s.state)),
      h("td.c-key", h("a", { href: sessionHash(s.key) }, s.key), !s.in_script ? h("span.badge.o-bad", { title: "the loaded script has no such session" }, "not in script") : null),
      h("td.c-num", { "data-label": "turn" }, s.requests ? String(s.turn) : "—"),
      h("td.c-step", { "data-label": "step" }, stepLabel(s), s.script_steps ? h("span.muted", ` / ${s.script_steps}`) : null),
      h("td.c-num", { "data-label": "requests" }, String(s.requests)),
      h("td.c-num" + (s.divergences ? ".bad" : ""), { "data-label": "diverged" }, String(s.divergences)),
      h("td.c-out", { "data-label": "last" }, s.gate
        ? h("button.btn.small.primary", { type: "button", onclick: (ev: Event) => { ev.stopPropagation(); void release(s.gate!); } }, `Release ${s.gate}`)
        : s.divergence ? h("span.ellipsis.bad", { title: s.divergence }, s.divergence) : s.outcome ? h("span.muted", s.reply || s.outcome) : null),
      h("td.c-time.muted", { "data-label": "at" }, s.last_at ? time(s.last_at) : "")));
  rows(trs);
  replace(main,
    h("div.toolbar", h("h1", "Sessions"), h("span.muted", Object.entries(countStates()).map(([k, n]) => `${n} ${k}`).join(" · "))),
    h("table.grid.sessions", { role: "grid" },
      h("thead", h("tr", ["state", "session", "turn", "step", "requests", "diverged", "last answer", "at"].map((c) => h("th", c)))),
      h("tbody", trs)),
    h("p.legend.muted", Object.entries(STATE_HELP).map(([k, v]) => h("span", h("b", k), " ", v))));
}

function countStates(): Record<string, number> {
  const c: Record<string, number> = {};
  for (const s of S.sessions) c[s.state] = (c[s.state] || 0) + 1;
  return c;
}

function sessionView(key: string): void {
  const s = S.sessions.find((x) => x.key === key);
  const es = S.requests.filter((e) => e.session === key && e.outcome !== "other");
  const outline = S.info.sessions?.find((o) => o.key === key);
  const head = h("div.toolbar",
    h("a.back", { href: "#/sessions", title: "Back (Esc)" }, "← Sessions"),
    h("h1.mono", key),
    s ? pill(s.state) : null,
    s ? h("span.muted", sessionLine(s)) : null,
    s?.gate ? h("button.btn.primary", { type: "button", onclick: () => void release(s.gate!) }, `Release ${s.gate}`) : null,
    h("a.btn.small", { href: "#/requests?session=" + encodeURIComponent(key) }, "Requests"));
  if (!es.length) {
    replace(main, head, empty("No requests in this session yet",
      outline ? h("p", `The script has ${plural(outline.steps.length, "step")}: `, outline.steps.map((st, i) => [i ? " → " : "", h("span.step", st.name)])) : null,
      h("p", "Send its key with every request:"),
      h("pre.cmd", `eval "$(understudy env --url ${location.origin} --session ${key})"`)));
    return;
  }
  const items: Child[] = [];
  let prev: Entry | undefined;
  let repeats: HTMLElement | null = null;
  for (const e of es) {
    const retry = !!prev && e.turn === prev.turn;
    const same = retry && !e.answer && !prev!.answer && !!e.divergence && e.divergence === prev!.divergence;
    if (same) {
      // The client retried a refused request: one line for every repeat.
      if (!repeats) items.push((repeats = h("div.repeats")));
      repeats.replaceChildren(h("span.badge.o-bad", "diverged"), ` retried ${repeats.childElementCount ? Number(repeats.dataset.n) + 1 : 1}× with the same divergence `,
        h("span.muted", `(last: #${e.seq}, ${time(e.at)})`));
      repeats.dataset.n = String(Number(repeats.dataset.n || 0) + 1);
    } else {
      repeats = null;
      items.push(exchange(e, retry));
    }
    prev = e;
  }
  if (s && (s.state === "waiting" || s.state === "streaming")) {
    items.push(h("div.next.muted", s.state === "waiting" ? "waiting for the client's next request…" : "streaming…"));
  }
  replace(main, head, h("div.convo", items));
}

function exchange(e: Entry, retry: boolean): HTMLElement {
  const results = e.history?.[e.turn] || [];
  const user: Child[] = [];
  if (!retry) {
    if (e.prompt) user.push(h("div.role", "user"), prompt(e.prompt));
    if (results.length) user.push(h("div.role", "tool results"), results.map(toolResult));
  }
  return h("section.ex" + (e.divergence ? ".ex-bad" : ""), { id: "r" + e.seq },
    h("div.ex-h",
      h("span.turn", retry ? `turn ${e.turn} · retry` : `turn ${e.turn}`),
      stepLabel(e),
      h("span.right",
        h("a.mono.muted", { href: "#/requests/" + e.seq, title: "the request" }, `#${e.seq}`),
        h("span.muted", time(e.at)),
        e.status ? h("span.mono", String(e.status)) : null,
        outcomeBadge(e))),
    user.length ? h("div.ex-user", user) : null,
    e.answer ? h("div.ex-asst", h("div.role", "assistant (script)"), answer(e.answer, e.status)) : null,
    held(e, (g) => void release(g)),
    e.divergence || e.outcome === "no_script" ? divergence(e) : null,
    !e.answer && !e.divergence && e.error ? h("div.diverge", h("p.diverge-msg", e.error)) : null);
}

// requests ---------------------------------------------------------------------

let filterBox: HTMLElement | null = null;
let filterState = { q: "", session: "", outcome: "", divergedOnly: false };

function filtered(): Entry[] {
  const f = filterState;
  const q = f.q.toLowerCase();
  return S.requests.filter((e) =>
    (!f.session || e.session === f.session) &&
    (f.outcome ? e.outcome === f.outcome : e.outcome !== "other") &&
    (!f.divergedOnly || !!e.divergence || e.outcome === "diverged" || e.outcome === "no_script") &&
    (!q || [e.path, e.session, e.step, e.reply, e.divergence, e.error, e.prompt, e.answer?.text, String(e.seq)].some((x) => x && x.toLowerCase().includes(q))));
}

function filters(r: Route): HTMLElement {
  filterState.session = r.q.get("session") || filterState.session;
  if (r.q.get("diverged") === "1") filterState.divergedOnly = true;
  const sync = () => {
    const p = new URLSearchParams();
    if (filterState.session) p.set("session", filterState.session);
    if (filterState.outcome) p.set("outcome", filterState.outcome);
    if (filterState.divergedOnly) p.set("diverged", "1");
    const seq = route().seq;
    history.replaceState(null, "", "#/requests" + (seq ? "/" + seq : "") + (p.toString() ? "?" + p : ""));
    render();
  };
  const search = h("input.search", { type: "search", placeholder: "Filter: text, step, seq  ( / )", "aria-label": "filter requests", value: filterState.q }) as HTMLInputElement;
  search.addEventListener("input", () => { filterState.q = search.value; selected = 0; sync(); });
  const sessions = h("select", { "aria-label": "session" }, h("option", { value: "" }, "all sessions"),
    S.sessions.map((s) => h("option", { value: s.key, selected: s.key === filterState.session }, s.key))) as HTMLSelectElement;
  sessions.addEventListener("change", () => { filterState.session = sessions.value; sync(); });
  const outcome = h("select", { "aria-label": "outcome" }, h("option", { value: "" }, "API requests"),
    OUTCOMES.map((o) => h("option", { value: o, selected: o === filterState.outcome }, o === "other" ? "other endpoints" : o.replace("_", " ")))) as HTMLSelectElement;
  outcome.addEventListener("change", () => { filterState.outcome = outcome.value; sync(); });
  const div = h("input", { type: "checkbox", checked: filterState.divergedOnly }) as HTMLInputElement;
  div.addEventListener("change", () => { filterState.divergedOnly = div.checked; sync(); });
  return h("div.filters", search, sessions, outcome, h("label.check", div, " divergences only"));
}

function requestsView(r: Route): void {
  if (!filterBox || !main.contains(filterBox)) filterBox = filters(r);
  else {
    // keep the filter box (and its focus); refresh the session list in place
    const sel = filterBox.querySelector("select") as HTMLSelectElement;
    const keys = S.sessions.map((s) => s.key);
    if (sel.options.length - 1 !== keys.length) {
      const fresh = filters(r);
      filterBox.replaceWith(fresh);
      filterBox = fresh;
    }
  }
  const list = filtered();
  const detail = r.seq !== undefined ? S.requests.find((e) => e.seq === r.seq) : undefined;
  if (detail) selected = Math.max(0, list.indexOf(detail));
  const trs = list.map((e) =>
    h("tr" + (e.divergence ? ".row-bad" : "") + (e.seq === r.seq ? ".open" : ""), { "data-href": requestHash(e.seq), tabindex: "-1" },
      h("td.c-num.mono", String(e.seq)),
      h("td.c-time.mono.muted", time(e.at)),
      h("td.c-path.mono", h("span.method", e.method), " ", e.path),
      h("td.c-key", e.session ? h("a", { href: sessionHash(e.session), onclick: (ev: Event) => ev.stopPropagation() }, e.session) : h("span.muted", "—")),
      h("td.c-num", { "data-label": "turn" }, e.outcome === "other" ? "" : String(e.turn)),
      h("td.c-step", { "data-label": "step" }, stepLabel(e)),
      h("td.c-kind.mono", { "data-label": "reply" }, e.reply || ""),
      h("td.c-num.mono", { "data-label": "status" }, e.status ? String(e.status) : ""),
      h("td.c-out", outcomeBadge(e))));
  rows(trs);
  const table = list.length
    ? h("table.grid.requests", { role: "grid" },
      h("thead", h("tr", ["#", "time", "request", "session", "turn", "step", "reply", "status", "outcome"].map((c) => h("th", c)))),
      h("tbody", trs))
    : S.requests.length ? empty("No request fits the filters") : noRequests();
  const pane = detail ? requestDetail(detail) : null;
  replace(main,
    h("div.toolbar", h("h1", "Requests"), h("span.muted", `${list.length} of ${S.requests.length}`)),
    filterBox,
    h("div.split" + (pane ? ".has-detail" : ""), h("div.list", table), pane));
  if (pane) pane.scrollTop = 0;
}

function kv(rows: [string, Child][]): HTMLElement {
  return h("dl.kv", rows.filter(([, v]) => v !== undefined && v !== null && v !== "").map(([k, v]) => [h("dt", k), h("dd", v)]));
}

function requestDetail(e: Entry): HTMLElement {
  const results = e.history?.[e.turn] || [];
  const earlier = (e.history || []).slice(1, e.turn);
  const sections: Child[] = [
    h("div.detail-h", h("h2.mono", `#${e.seq}`), outcomeBadge(e),
      h("a.btn.small.right", { href: "#/requests" + (location.hash.includes("?") ? "?" + location.hash.split("?")[1] : ""), title: "Close (Esc)", "aria-label": "close" }, "✕")),
    kv([
      ["request", h("span.mono", `${e.method} ${e.path}`)],
      ["time", time(e.at)],
      ["status", e.status ? String(e.status) : ""],
      ["session", e.session ? [h("a", { href: sessionHash(e.session) }, e.session), h("span.muted", ` (from the ${(e.session_source || "").replace("_", " ")})`)] : ""],
      ["turn", e.outcome === "other" ? "" : String(e.turn)],
      ["step", e.step ? stepLabel(e) : ""],
      ["model", e.model],
      ["stream", e.outcome === "other" ? "" : e.stream ? "yes" : "no"],
      ["tools offered", e.tools_offered ? String(e.tools_offered) : ""],
      ["cwd", e.cwd ? h("span.mono", e.cwd) : ""],
    ]),
  ];
  const hl = held(e, (g) => void release(g));
  if (hl) sections.push(hl);
  if (e.divergence || e.outcome === "no_script" || e.outcome === "bad_request") sections.push(e.divergence ? divergence(e) : h("div.diverge", h("p.diverge-msg", e.error || "")));
  if (e.answer) sections.push(h("h3", "Answer (script)"), answer(e.answer, e.status));
  if (e.prompt) sections.push(h("h3", "Prompt"), prompt(e.prompt));
  if (results.length) sections.push(h("h3", "Tool results"), results.map(toolResult));
  if (e.trace?.length) sections.push(h("h3", "Trace"), h("ol.trace", e.trace.map((t) => h("li", t))));
  if (earlier.length) sections.push(h("details", h("summary", `Earlier turns (${earlier.length})`),
    earlier.map((rs, i) => [h("div.role", `turn ${i + 1}`), rs.length ? rs.map(toolResult) : h("p.muted", "no tool results")])));
  if (e.headers) sections.push(h("details", h("summary", "Headers"),
    kv(Object.entries(e.headers).sort().map(([k, v]) => [k, h("span.mono", v)]))));
  return h("aside.detail", { "aria-label": `request ${e.seq}` }, sections);
}

// gates ------------------------------------------------------------------------

function gatesView(): void {
  if (!S.gates.length) {
    replace(main, h("div.toolbar", h("h1", "Gates")), empty("No gates yet",
      h("p", "A reply with ", h("code", "stall: {gate: NAME}"), " waits here until it is released.")));
    return;
  }
  const trs = S.gates.map((g) => {
    const who = S.sessions.filter((s) => s.gate === g.name);
    return h("tr" + (g.waiting ? ".row-hold" : ""), { tabindex: "-1", "data-gate": g.name },
      h("td.c-key.mono", g.name),
      h("td", g.released ? h("span.badge.o-ok", "open") : h("span.badge.o-hold", "closed")),
      h("td.c-num", { "data-label": "waiting" }, String(g.waiting)),
      h("td.c-num", { "data-label": "passed" }, String(g.passed)),
      h("td", { "data-label": "held" }, who.map((s, i) => [i ? ", " : "", h("a", { href: sessionHash(s.key) }, s.key)])),
      h("td.c-out", g.released && !g.waiting ? h("span.muted", "later stalls pass at once") :
        h("button.btn.small.primary", { type: "button", onclick: () => void release(g.name) }, "Release")));
  });
  rows(trs);
  replace(main, h("div.toolbar", h("h1", "Gates")),
    h("table.grid.gates", { role: "grid" },
      h("thead", h("tr", ["gate", "state", "waiting", "passed", "held sessions", ""].map((c) => h("th", c)))), h("tbody", trs)));
}

// script -----------------------------------------------------------------------

let editing = false;
let draft = "";
let draftCheck: { ok: boolean; problems: Problem[]; sessions: string[] } | null = null;
let checkTimer = 0;

function scriptView(): void {
  const info = S.info;
  const hits = new Map<number, string>();
  const counts = new Map<number, number>();
  for (const o of info.sessions || []) {
    for (const st of o.steps) {
      if (!st.line) continue;
      const es = S.requests.filter((e) => e.session === o.key && e.step === st.name);
      if (es.length) counts.set(st.line, es.length);
      if (es.some((e) => e.divergence)) hits.set(st.line, "l-bad");
      else if (es.length) hits.set(st.line, "l-hit");
    }
  }
  const head = h("div.toolbar", h("h1", "Script"),
    info.loaded ? h("span.badge.o-ok", "checks") : h("span.badge.o-warn", "none loaded"),
    info.loaded ? h("span.muted", `divergence: ${info.divergence}`) : null,
    info.provenance ? h("span.muted", `${info.provenance.source || ""}${info.provenance.cli_version ? " · Claude Code " + info.provenance.cli_version : ""}`) : null,
    h("span.right", editing
      ? [h("button.btn", { type: "button", onclick: () => { editing = false; render(); } }, "Cancel"),
        h("button.btn.primary", { type: "button", disabled: !draftCheck?.ok, onclick: () => void loadDraft() }, "Load")]
      : h("button.btn", { type: "button", onclick: () => { editing = true; draft = S.source || draft || "schema: understudy/v1\nsessions:\n  default:\n    steps:\n      - name: answer\n        reply: {message: {text: \"Hello.\"}}\n"; void check(); render(); } }, info.loaded ? "Edit" : "Write a script")));
  if (editing) {
    const ta = document.getElementById("draft") as HTMLTextAreaElement | null;
    const box = ta ?? (h("textarea.editor", { id: "draft", spellcheck: "false", "aria-label": "script text" }) as HTMLTextAreaElement);
    if (!ta) {
      box.value = draft;
      box.addEventListener("input", () => {
        draft = box.value;
        clearTimeout(checkTimer);
        checkTimer = window.setTimeout(() => void check(), 250);
      });
      box.addEventListener("keydown", (ev) => {
        if (ev.key === "Tab") {
          ev.preventDefault();
          box.setRangeText("  ", box.selectionStart, box.selectionEnd, "end");
          box.dispatchEvent(new Event("input"));
        }
      });
    }
    replace(main, head, h("div.editwrap", box, problemsPane()));
    if (!ta) box.focus();
    return;
  }
  if (!info.loaded) {
    replace(main, head, empty("No script loaded",
      h("p", "Load one with ", h("code", "understudy serve --script FILE"), ", ", h("code", `PUT ${BASE}script`), ", the MCP tool load_script, or Write a script above.")));
    return;
  }
  const code = yaml(S.source, hits);
  for (const [line, n] of counts) {
    const ln = code.querySelector(`#L${line} .ln`);
    if (ln) ln.setAttribute("data-hits", "×" + n);
  }
  replace(main, head,
    h("div.scriptwrap",
      h("nav.outline", { "aria-label": "outline" }, (info.sessions || []).map((o) =>
        h("div.ol-s",
          h("a.mono", { href: "#L" + (o.line || 1), onclick: (ev: Event) => jump(ev, o.line) }, o.key),
          h("ol", o.steps.map((st) => h("li", h("a", { href: "#L" + (st.line || 1), onclick: (ev: Event) => jump(ev, st.line) }, st.name,
            counts.get(st.line || 0) ? h("span.hits" + (hits.get(st.line || 0) === "l-bad" ? ".bad" : ""), String(counts.get(st.line || 0))) : null))))))),
      code));
}

function jump(ev: Event, line?: number): void {
  ev.preventDefault();
  const el = document.getElementById("L" + (line || 1));
  if (!el) return;
  el.scrollIntoView({ block: "center" });
  el.classList.add("flash");
  setTimeout(() => el.classList.remove("flash"), 1200);
}

function problemsPane(): HTMLElement {
  if (!draftCheck) return h("div.problems.muted", "checking…");
  if (draftCheck.ok) return h("div.problems.ok", h("strong", "The script checks"), ` · ${plural(draftCheck.sessions.length, "session")}: ${draftCheck.sessions.join(", ")}`);
  return h("div.problems",
    h("strong.bad", plural(draftCheck.problems.length, "problem")),
    h("ul", draftCheck.problems.map((p) => h("li",
      h("button.link.mono", { type: "button", onclick: () => gotoLine(p.line) }, p.line ? `line ${p.line}` : "file"),
      " ", h("span.mono.muted", p.path), h("div", p.message, h("span.rule", p.rule)), h("div.fix", "Fix: ", p.fix)))));
}

function gotoLine(line?: number): void {
  const ta = document.getElementById("draft") as HTMLTextAreaElement | null;
  if (!ta || !line) return;
  const lines = ta.value.split("\n");
  const start = lines.slice(0, line - 1).reduce((n, l) => n + l.length + 1, 0);
  ta.focus();
  ta.setSelectionRange(start, start + (lines[line - 1] || "").length);
}

async function check(): Promise<void> {
  try {
    draftCheck = await api.validate(draft);
  } catch (e) {
    draftCheck = { ok: false, sessions: [], problems: [{ path: "", rule: "syntax", message: (e as Error).message, fix: "check the server" }] };
  }
  const pane = main.querySelector(".problems");
  if (pane) pane.replaceWith(problemsPane());
  const load = main.querySelector(".toolbar .btn.primary") as HTMLButtonElement | null;
  if (load) load.disabled = !draftCheck.ok;
}

async function loadDraft(): Promise<void> {
  try {
    const r = await api.load(draft);
    editing = false;
    toast(`Loaded: ${plural(r.sessions.length, "session")}`);
    S.source = "";
    await refresh();
  } catch (e) {
    toast((e as Error).message, true);
  }
}

// ---- keyboard ----------------------------------------------------------------

function help(show: boolean): void {
  document.getElementById("help")?.remove();
  if (!show) return;
  const keys: [string, string][] = [["1 2 3 4", "Sessions, Requests, Gates, Script"], ["j / k  ↓ / ↑", "move the selection"], ["Enter", "open the selected row"],
    ["Esc", "back, or close the request"], ["/", "filter requests"], ["r", "release the gate of the open or selected session"], ["t", "light or dark"], ["?", "this help"]];
  const box = h("div.overlay", { id: "help", role: "dialog", "aria-label": "keyboard shortcuts", onclick: (ev: Event) => { if (ev.target === box) help(false); } },
    h("div.dialog", h("h2", "Keyboard"), h("dl.kv", keys.map(([k, v]) => [h("dt", h("kbd", k)), h("dd", v)])),
      h("button.btn", { type: "button", onclick: () => help(false) }, "Close")));
  document.body.append(box);
  (box.querySelector("button") as HTMLElement).focus();
}

document.addEventListener("keydown", (ev) => {
  const t = ev.target as HTMLElement;
  const typing = t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT";
  if (ev.key === "Escape") {
    if (document.getElementById("help")) return help(false);
    if (typing) return t.blur();
    const r = route();
    if (r.view === "sessions" && r.key !== undefined) go("#/sessions");
    else if (r.view === "requests" && r.seq !== undefined) go("#/requests" + (location.hash.includes("?") ? "?" + location.hash.split("?")[1] : ""));
    return;
  }
  if (typing || ev.metaKey || ev.ctrlKey || ev.altKey) return;
  const views: View[] = ["sessions", "requests", "gates", "script"];
  const trs = Array.from(main.querySelectorAll<HTMLElement>("tbody tr"));
  switch (ev.key) {
    case "1": case "2": case "3": case "4":
      go("#/" + views[Number(ev.key) - 1]);
      break;
    case "j": case "ArrowDown": case "k": case "ArrowUp": {
      if (!trs.length) return;
      ev.preventDefault();
      selected = Math.max(0, Math.min(trs.length - 1, selected + (ev.key === "j" || ev.key === "ArrowDown" ? 1 : -1)));
      rows(trs);
      trs[selected].scrollIntoView({ block: "nearest" });
      if (route().view === "requests" && route().seq !== undefined) go(trs[selected].dataset.href!);
      break;
    }
    case "Enter": {
      const href = trs[selected]?.dataset.href;
      if (href) go(href);
      break;
    }
    case "/": {
      const s = main.querySelector<HTMLInputElement>("input.search");
      if (s) { ev.preventDefault(); s.focus(); } else go("#/requests");
      break;
    }
    case "r": {
      const r = route();
      let gate: string | undefined;
      if (r.view === "sessions") gate = r.key !== undefined ? S.sessions.find((s) => s.key === r.key)?.gate : undefined;
      if (r.view === "gates") gate = trs[selected]?.dataset.gate;
      if (!gate && r.view === "sessions" && r.key === undefined) {
        const key = trs[selected]?.dataset.href?.split("/").pop();
        gate = S.sessions.find((s) => sessionHash(s.key).endsWith("/" + key))?.gate;
      }
      if (gate) void release(gate);
      break;
    }
    case "t":
      toggleTheme();
      break;
    case "?":
      help(true);
      break;
  }
});

main.addEventListener("click", (ev) => {
  const t = ev.target as HTMLElement;
  if (t.closest("a, button, input, select, textarea, summary")) return;
  const tr = t.closest<HTMLElement>("tr[data-href]");
  if (tr) go(tr.dataset.href!);
});

// ---- start -------------------------------------------------------------------

applyTheme(store("understudy-theme"));
window.addEventListener("hashchange", () => {
  if (route().view !== "requests") filterBox = null;
  render();
  if (route().view === "sessions" && route().key) main.scrollTop = 0;
});
render();
void refresh();
connect();
