// Small, safe highlighters: they build DOM nodes, never HTML strings.
import { h } from "./dom";

type Tok = [cls: string, text: string];

const scalar = (v: string): string => {
  if (/^(true|false|null|~)$/.test(v)) return "kw";
  if (/^-?\d+(\.\d+)?$/.test(v)) return "num";
  return "str";
};

// yamlLine splits one YAML line into tokens.
export function yamlLine(line: string): Tok[] {
  const out: Tok[] = [];
  let rest = line;
  const ind = /^\s*(- )*/.exec(rest)![0];
  if (ind) out.push(["punc", ind]), (rest = rest.slice(ind.length));
  if (rest.startsWith("#")) return out.concat([["com", rest]]);
  const key = /^("[^"]*"|'[^']*'|[^\s:#{}[\],][^:#{}[\],]*?)(:)(\s|$)/.exec(rest);
  if (key) {
    out.push(["key", key[1]], ["punc", key[2]]);
    rest = rest.slice(key[1].length + 1);
  }
  // The value: flow punctuation, quoted strings, comments, placeholders, words.
  const re = /(\s+)|("(?:[^"\\]|\\.)*"?|'[^']*'?)|(#.*$)|([{}[\],]|: )|(\{\{\w+\}\})|([^\s{}[\],:#"']+(?::(?! )[^\s{}[\],]*)*|:)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(rest))) {
    if (m[1]) out.push(["", m[1]]);
    else if (m[2]) out.push(["str", m[2]]);
    else if (m[3]) out.push(["com", m[3]]);
    else if (m[4]) out.push(["punc", m[4]]);
    else if (m[5]) out.push(["ph", m[5]]);
    else if (m[6]) {
      // A word followed by ": " inside a flow map is a key.
      const after = rest.slice(re.lastIndex);
      out.push([/^:\s/.test(after) ? "key" : scalar(m[6]), m[6]]);
    }
  }
  return out;
}

function tokens(toks: Tok[]): Node[] {
  return toks.map(([cls, text]) => (cls ? h(`span.t-${cls}`, text) : document.createTextNode(text)));
}

// yaml renders a whole YAML text as numbered lines; marks maps a line to a CSS class.
export function yaml(src: string, marks: Map<number, string> = new Map()): HTMLElement {
  const lines = src.replace(/\n$/, "").split("\n");
  return h("div.code", { role: "table", "aria-label": "script" },
    lines.map((l, i) =>
      h("div.line" + (marks.has(i + 1) ? "." + marks.get(i + 1) : ""), { id: `L${i + 1}`, role: "row" },
        h("span.ln", { "aria-hidden": "true" }, String(i + 1)),
        h("span.lc", tokens(yamlLine(l))))));
}

// json renders a value as indented, highlighted JSON.
export function json(v: unknown): HTMLElement {
  const text = JSON.stringify(v, null, 2) ?? "null";
  const out: Node[] = [];
  const re = /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|(true|false|null)|([{}[\],])|(\s+)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) {
    if (m[1]) {
      out.push(h(m[2] ? "span.t-key" : "span.t-str", m[1]));
      if (m[2]) out.push(h("span.t-punc", m[2]));
    } else if (m[3]) out.push(h("span.t-num", m[3]));
    else if (m[4]) out.push(h("span.t-kw", m[4]));
    else if (m[5]) out.push(h("span.t-punc", m[5]));
    else out.push(document.createTextNode(m[6]));
  }
  return h("pre.json", out);
}
