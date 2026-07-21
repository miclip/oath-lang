// Generate the browser Z3 assets for the /try playground from the installed
// z3-solver package (#34). These are large, derived, and reproducible from a
// declared dependency, so they are gitignored and regenerated at build time
// (prebuild) and dev startup (predev) rather than committed — unlike oath.wasm,
// which a Node build environment (Vercel) cannot produce and so stays committed.
//
// Three files land in public/pgrt/z3/:
//   z3-built.js / z3-built.wasm  — Emscripten module, copied verbatim; MUST load
//                                  as its own script so Emscripten resolves its
//                                  wasm and pthread workers (bundling breaks it).
//   z3-solver.js                 — the high-level wrapper, esbuilt to an IIFE that
//                                  exposes globalThis.__z3solverInit.
import { copyFileSync, mkdirSync, existsSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { createRequire } from "node:module";
import { build } from "esbuild";

const require = createRequire(import.meta.url);
const buildDir = dirname(require.resolve("z3-solver/build/browser.js"));
const outDir = join(process.cwd(), "public/pgrt/z3");
mkdirSync(outDir, { recursive: true });

const wasmOut = join(outDir, "z3-built.wasm");
// Skip the 32MB copy + rebuild when assets are already present and current,
// unless FORCE is set (the build path forces a clean regenerate).
const fresh =
  !process.env.FORCE_Z3_ASSETS &&
  existsSync(wasmOut) &&
  existsSync(join(outDir, "z3-solver.js")) &&
  statSync(wasmOut).mtimeMs >= statSync(join(buildDir, "z3-built.wasm")).mtimeMs;
if (fresh) {
  console.log("z3 assets already current — skipping (set FORCE_Z3_ASSETS=1 to rebuild)");
  process.exit(0);
}

copyFileSync(join(buildDir, "z3-built.js"), join(outDir, "z3-built.js"));
copyFileSync(join(buildDir, "z3-built.wasm"), wasmOut);

await build({
  stdin: {
    contents: 'globalThis.__z3solverInit = require("z3-solver/build/browser").init;',
    resolveDir: process.cwd(),
    loader: "js",
  },
  bundle: true,
  format: "iife",
  platform: "browser",
  outfile: join(outDir, "z3-solver.js"),
  logLevel: "warning",
});

console.log("z3 assets generated in public/pgrt/z3/");
