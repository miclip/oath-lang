// Proof that the committed playground engine runs the REAL kernel end to end
// (#34). Node stands in for the browser: identical wasm, identical MemFS, so a
// green run here means the compute path works. Requires the build artifacts
// (oath.wasm, corpus-snapshot.json) — run `make playground-assets` first.
//   node website/lib/playground/kernel.test.mjs
import { MemFS } from "./memfs.js";
import fs from "fs"; import path from "path";
import { readFileSync } from "fs";

const HERE = path.dirname(new URL(import.meta.url).pathname);
const PUB = path.join(HERE, "../../public/pgrt");
for (const a of ["oath.wasm", "wasm_exec.js", "corpus-snapshot.json"]) {
  if (!fs.existsSync(path.join(PUB, a))) {
    console.log(`SKIP: ${a} not built — run \`make playground-assets\` first`);
    process.exit(0);
  }
}
const snap = JSON.parse(readFileSync(path.join(PUB, "corpus-snapshot.json"), "utf8"));
const files = { [snap.root]: "DIR", [snap.root + "/objects"]: "DIR", [snap.root + "/meta"]: "DIR" };
for (const [p, b64] of Object.entries(snap.files)) files[p] = new Uint8Array(Buffer.from(b64, "base64"));
globalThis.fs = new MemFS(files);
globalThis.path = path;

await import(path.join(PUB, "wasm_exec.js")); // defines globalThis.Go (CJS via global)
// wasm_exec.js is a classic script; load it by eval since Node ESM won't run it as a module:
if (!globalThis.Go) { const src = readFileSync(path.join(PUB, "wasm_exec.js"), "utf8"); (0, eval)(src); }
const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(readFileSync(path.join(PUB, "oath.wasm")), go.importObject);
go.run(instance);
const check = (src) => JSON.parse(globalThis.oathCheck(snap.root, src));

let fail = 0;
const expect = (label, cond, got) => { console.log(`${cond ? "PASS" : "FAIL"}  ${label}${cond ? "" : "  got: " + got}`); if (!cond) fail++; };

// grab a top-level (defn ...) form from an example file by paren matching
const form = (file, kw) => {
  const s = readFileSync(path.join(HERE, "../../../examples", file), "utf8");
  let i = s.indexOf(kw), d = 0;
  for (let k = i; k < s.length; k++) { if (s[k] === "(") d++; else if (s[k] === ")") { if (--d === 0) return s.slice(i, k + 1); } }
};

const sortRes = check(form("sort.oath", "(defn sort")); const sort = sortRes.reports[0];
expect("sort verbatim → recorded PROVEN 7/7, ok", sortRes.ok === true && sort.status === "accepted" && /PROVEN \(all 7/.test(sort.guarantee || ""), JSON.stringify([sortRes.ok, sort.guarantee]));

const brRes = check(form("bad_reverse.oath", "(defn")); const br = brRes.reports[0];
const brCounter = (br.props || []).find(p => p.failed)?.counterexample;
expect("bad-reverse → FALSIFIED, ok=false, counterexample", brRes.ok === false && br.status === "falsified" && !!brCounter, JSON.stringify([brRes.ok, br.status]));

const novelRes = check("(defn dbl [] [(x Int)] Int (+ x x) (prop is-double [(x Int)] (== (dbl x) (* 2 x))))");
const novel = novelRes.reports[0];
expect("novel valid def → tested, total, ok",
  novelRes.ok === true && novel.status === "accepted" && /tested/.test(novel.guarantee || "") &&
  (novel.termination === "structural" || novel.termination === "nonrecursive"), JSON.stringify([novelRes.ok, novel.guarantee, novel.termination]));

const wrongRes = check("(defn d [] [(x Int)] Int (+ x x) (prop bad [(x Int)] (== (d x) x)))"); const wrong = wrongRes.reports[0];
expect("false prop → falsified, ok=false, counterexample", wrongRes.ok === false && wrong.status === "falsified" && !!(wrong.props || []).find(p => p.failed)?.counterexample, JSON.stringify([wrongRes.ok, wrong.status]));

console.log(fail ? `\n${fail} FAILED` : "\nall playground engine checks passed");
process.exit(fail ? 1 : 0);
