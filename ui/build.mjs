// Builds web/ into dist/: one app.js, one app.css, index.html and the icons.
// The output is committed; CI rebuilds it and fails when it differs.
import { build } from "esbuild";
import { copyFileSync, mkdirSync } from "node:fs";

mkdirSync("dist", { recursive: true });
const common = { bundle: true, minify: true, legalComments: "none", logLevel: "warning" };
await build({ ...common, entryPoints: ["web/src/main.ts"], outfile: "dist/app.js", format: "iife", target: "es2022" });
await build({ ...common, entryPoints: ["web/style.css"], outfile: "dist/app.css" });
for (const f of ["index.html", "logo.svg", "favicon-32.png"]) copyFileSync("web/" + f, "dist/" + f);
