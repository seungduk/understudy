// The control API this UI reads: types mirror the server's JSON.

export const BASE = "/_understudy/";

export type Outcome =
  | "pending" | "served" | "cut_close" | "cut_reset" | "client_gone"
  | "diverged" | "no_script" | "bad_request" | "other";

export const OUTCOMES: Outcome[] = ["served", "pending", "diverged", "no_script", "bad_request", "cut_close", "cut_reset", "client_gone", "other"];

export type State = "idle" | "waiting" | "streaming" | "stalled" | "diverged" | "done";

export interface Result { tool?: string; is_error?: boolean; text: string }
export interface Call { tool: string; input: unknown }
export interface Answer {
  kind: "message" | "tool_use" | "http_error" | "stream_error" | "limit";
  thinking?: string; text?: string; stop?: string; calls?: Call[];
  error?: string; message?: string; retry?: string;
  window?: string; resets_in_seconds?: number; faults?: string[];
}
export interface Hold { gate?: string; seconds?: number; since: string }

export interface Entry {
  seq: number; at: string; method: string; path: string; status?: number; outcome: Outcome;
  model?: string; stream?: boolean; session?: string; session_source?: string;
  turn: number; step?: string; branch?: string; attempt?: number; reply?: string;
  divergence?: string; error?: string; trace?: string[]; tools_offered?: number; cwd?: string;
  history?: Result[][]; headers?: Record<string, string>;
  prompt?: string; answer?: Answer; held?: Hold; expected?: string[]; actual?: string[];
}

export interface Session {
  key: string; state: State; in_script: boolean; script_steps?: number;
  requests: number; divergences: number; turn: number; step?: string; branch?: string;
  attempt?: number; reply?: string; outcome?: Outcome; last_seq?: number; last_at?: string;
  gate?: string; divergence?: string;
}

export interface Gate { name: string; released: boolean; waiting: number; passed: number }
export interface Health { ok: boolean; version: string; script_loaded: boolean }
export interface Clock { mode: "real" | "virtual"; now: string }
export interface Problem { path: string; line?: number; rule: string; message: string; fix: string }
export interface Validation { ok: boolean; sessions: string[]; problems: Problem[] }
export interface Outline { key: string; line?: number; steps: { name: string; line?: number }[] }
export interface ScriptInfo {
  loaded: boolean; divergence?: string; sessions?: Outline[];
  provenance?: Record<string, string> | null;
}

async function req<T>(method: string, path: string, body?: string): Promise<T> {
  const r = await fetch(BASE + path, { method, body, headers: body ? { "Content-Type": "application/yaml" } : {} });
  const text = await r.text();
  if (!r.ok) {
    let msg = `${method} ${path}: ${r.status}`;
    try {
      const j = JSON.parse(text);
      throw Object.assign(new Error(j.error || msg), { fix: j.fix as string | undefined, problems: j.problems as Problem[] | undefined });
    } catch (e) {
      if (e instanceof SyntaxError) throw new Error(msg + " " + text);
      throw e;
    }
  }
  return (path === "script" ? text : JSON.parse(text)) as T;
}

export const api = {
  health: () => req<Health>("GET", "health"),
  clock: () => req<Clock>("GET", "clock"),
  requests: () => req<{ requests: Entry[] }>("GET", "requests").then((r) => r.requests || []),
  sessions: () => req<{ sessions: Session[] }>("GET", "sessions").then((r) => r.sessions || []),
  gates: () => req<{ gates: Gate[] }>("GET", "gates").then((r) => r.gates || []),
  scriptInfo: () => req<ScriptInfo>("GET", "script/info"),
  script: () => req<string>("GET", "script").catch(() => ""),
  validate: (src: string) => req<Validation>("POST", "validate", src),
  load: (src: string) => req<{ loaded: boolean; sessions: string[] }>("PUT", "script", src),
  release: (gate: string) => req<{ released: string; waiting: number }>("POST", `gates/${encodeURIComponent(gate)}/release`),
  reset: () => req<{ reset: boolean }>("POST", "reset"),
};
