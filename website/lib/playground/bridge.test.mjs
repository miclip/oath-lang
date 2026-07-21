// Proof that the wasm kernel proves NOVEL definitions client-side through the
// Z3 bridge (#34) — the whole point of the async worker architecture. Node
// stands in for the browser: identical wasm, MemFS, SharedArrayBuffer, and
// z3-solver@4.16.0. Requires `make playground-assets` and z3-solver.
//   cd website && node lib/playground/bridge.test.mjs
import { Worker } from "node:worker_threads";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
for (const a of ["oath.wasm", "wasm_exec.js", "corpus-snapshot.json"]) {
  if (!fs.existsSync(path.join(HERE, "../../public/pgrt", a))) {
    console.log(`SKIP: ${a} not built — run \`make playground-assets\` first`);
    process.exit(0);
  }
}

const k = new Worker(new URL("./kernel-worker.mjs", import.meta.url));
await new Promise((r) => k.once("message", (m) => m === "kernel-ready" && r()));
const run = (source, name) => new Promise((r) => { k.once("message", r); k.postMessage({ source, name }); });

let fail = 0;
const expect = (label, cond, got) => { console.log(`${cond ? "PASS" : "FAIL"}  ${label}${cond ? "" : "  got: " + got}`); if (!cond) fail++; };

// A novel def, provable by a DIRECT z3 call.
const direct = await run("(defn tw [] [(x Int)] Int (+ x x) (prop is-double [(x Int)] (== (tw x) (* 2 x))))", "tw");
expect("novel def proven client-side via z3 bridge (direct)", /∎ PROVEN\s+is-double/.test(direct.proof.proofReport || ""), JSON.stringify(direct.proof).slice(0, 200));

// A novel recursive def whose property needs INDUCTION (multiple sequential z3 calls).
const induct = await run("(defn cnt [] [(xs (List Int))] Int (match xs ((Nil) 0) ((Cons h t) (+ 1 (cnt t)))) (prop nonneg [(xs (List Int))] (<= 0 (cnt xs))))", "cnt");
expect("novel def proven client-side via z3 bridge (induction)", /∎ PROVEN\s+nonneg\s+induction/.test(induct.proof.proofReport || ""), JSON.stringify(induct.proof).slice(0, 200));

console.log(fail ? `\n${fail} FAILED` : "\nz3 bridge: novel definitions proven client-side ✓");
await k.terminate();
process.exit(fail ? 1 : 0);
