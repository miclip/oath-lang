// Behavioural conformance for the SERVED playground kernel (#145).
//
// THE CONTRACT, stated narrowly and deliberately not widened:
//
//   the wasm artifact the browser actually downloads must re-elaborate the
//   current corpus to the identities `codebase/names.json` records, through its
//   own syscall/js boundary.
//
// **THIS IS NOT A FRESHNESS CHECK, and nothing here may be read as one.** It is
// green whenever the served binary AGREES with the reference on what the corpus
// exercises — including when that binary is months old. A kernel change that
// alters nothing the corpus reaches passes correctly, because this is a claim
// about observable behaviour. #133's `lex` UTF-8 guard is exactly that shape: it
// fires only on invalid input, which no corpus source contains. So this gate
// would have stayed GREEN over the staleness that opened #145. Artifact
// freshness is a different obligation with a different owner; it is tracked
// separately and is not discharged here.
//
// What it DOES cover, which nothing else does: the wasm is a THIRD compilation
// of the kernel — its own build tags, its own `syscall/js` string boundary —
// and `oathrs/conformance.sh` says nothing about it. Go-vs-Rust N-version
// agreement does not imply Go-vs-wasm agreement.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { MemFS } from "../website/lib/playground/memfs.js";
import { guardKernelExports } from "../website/lib/playground/lossless.js";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const PUB = path.join(ROOT, "website/public/pgrt");

// The artifacts are COMMITTED, so absence is a real failure rather than a reason
// to skip. A gate that skips silently measures nothing while reporting success —
// the failure mode this repo keeps finding, and the reason kernel.test.mjs's
// SKIP is right there (it needs a 16MB build) and wrong here (it does not).
for (const a of ["oath.wasm", "wasm_exec.js", "corpus-snapshot.json"]) {
  if (!fs.existsSync(path.join(PUB, a))) {
    console.error(`ERROR: ${a} is missing from website/public/pgrt — it is a committed artifact; run 'make playground-assets'`);
    process.exit(1);
  }
}

// Boot the SERVED artifact, not a fresh build: the claim is about the bytes the
// browser downloads.
const snap = JSON.parse(fs.readFileSync(path.join(PUB, "corpus-snapshot.json"), "utf8"));
const files = { [snap.root]: "DIR", [snap.root + "/objects"]: "DIR", [snap.root + "/meta"]: "DIR" };
for (const [p, b64] of Object.entries(snap.files)) files[p] = new Uint8Array(Buffer.from(b64, "base64"));
globalThis.fs = new MemFS(files);
globalThis.path = path;

if (!globalThis.Go) { (0, eval)(fs.readFileSync(path.join(PUB, "wasm_exec.js"), "utf8")); }
const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(fs.readFileSync(path.join(PUB, "oath.wasm")), go.importObject);
go.run(instance);
guardKernelExports(globalThis); // the JS-side ADMIT boundary (#133)

// THE UNIVERSE COMES FROM THE CLAIM, not from a hand-kept list. "The corpus" is
// examples/*.oath PLUS apps/*/*.oath (CLAUDE.md), so it is globbed rather than
// enumerated — a literal list here would drift from the Makefile's the way
// PROVABLE/TESTED_ONLY already did, leaving definitions silently unchecked.
const allSources = [
  ...fs.readdirSync(path.join(ROOT, "examples")).filter((f) => f.endsWith(".oath")).map((f) => `examples/${f}`),
  ...fs.readdirSync(path.join(ROOT, "apps"), { withFileTypes: true })
    .filter((e) => e.isDirectory())
    .flatMap((e) => fs.readdirSync(path.join(ROOT, "apps", e.name)).filter((f) => f.endsWith(".oath")).map((f) => `apps/${e.name}/${f}`)),
].sort();

// A DOCUMENTED, LOUD exclusion — never a silent one. This gate found the entry
// on its first run: `spin` recurses without descending, and where the native
// kernel's depth guard converts that into an honest FALSIFIED verdict, the wasm
// build overflows first and takes the Go runtime with it
// (`fatal error: runtime: mcall called on m->g0 stack`). Every file after it in
// the same process is then unmeasurable, so one crash would silently gut the
// gate's coverage. Excluded until the underlying defect is fixed; the count is
// printed on every run so the hole cannot pass for coverage. See #147.
// Each entry names the definitions it removes from reach, so the exact contract
// below stays derivable rather than hand-maintained.
//
// EMPTY, and it should stay that way. Its one entry was `examples/nontotal.oath`,
// excluded because the wasm build CRASHED on non-terminating input rather than
// falsifying it — the defect this gate found on its first run. #147 fixed that
// (the depth guard now fires on wasm), so the exclusion was retired with it. An
// exclusion that outlives its cause is a permanent hole wearing a reason.
const EXCLUDED = new Map([]);
const sources = allSources.filter((s) => !EXCLUDED.has(s));

const expected = JSON.parse(fs.readFileSync(path.join(ROOT, "codebase/names.json"), "utf8"));
const changed = [], unknown = [], errored = [];
const reached = new Map();

for (const rel of sources) {
  const src = fs.readFileSync(path.join(ROOT, rel), "utf8");
  let res;
  try {
    res = JSON.parse(globalThis.oathCheck(snap.root, src));
  } catch (e) {
    errored.push([rel, String(e)]);
    continue;
  }
  // An `error` response IS a failure. The deliberately-broken exhibits do not
  // produce one — falsification and rejection are report STATUS, not an error —
  // so every corpus file elaborates cleanly today, and a wasm-only parser or
  // typechecker regression would show up here. Treating it as benign was the
  // first draft's defect: it let a file contribute zero definitions silently.
  if (res.error) {
    errored.push([rel, `kernel returned an error: ${res.error}`]);
  }
  for (const r of res.reports || []) {
    if (!r.hash || !r.name) continue;
    reached.set(r.name, r.hash);
    if (!(r.name in expected)) { unknown.push([rel, r.name, r.hash]); continue; }
    if (expected[r.name] !== r.hash) changed.push([rel, r.name, expected[r.name], r.hash]);
  }
}

// THE SET IS PINNED, NOT FLOORED — and the difference is not pedantry. The first
// draft asserted only `reached.size >= 100`, which a regression dropping 109 of
// 210 definitions would have passed. A floor is a PROXY for the claim; the claim
// is that the corpus re-elaborates to the recorded identities, so the universe
// must be the recorded names themselves, minus exactly what an exclusion removes.
//
// Self-maintaining in the right direction: a definition added to the corpus
// appears in names.json, so the gate demands it on the day it is written. Caught
// by review.
const excludedNames = new Set([...EXCLUDED.values()].flatMap((e) => e.names));
const mustReach = Object.keys(expected).filter((n) => !excludedNames.has(n));
const missing = mustReach.filter((n) => !reached.has(n));

// THE #147 REGRESSION WITNESS, asserted by name. Non-terminating input must
// reach the depth guard and FALSIFY — the outcome examples/nontotal.oath's own
// comment promises — rather than exhausting the JS host stack and killing the
// runtime. The corpus sweep above would also catch a regression here, but only
// as an anonymous "threw while elaborating"; naming it means the next person
// who raises maxEvalDepth for GOOS=js learns why they cannot.
let spinVerdict = "kernel did not survive", spinAlive = false;
try {
  const r = JSON.parse(globalThis.oathCheck(snap.root,
    "(defn spin147 [] [(x Int)] Int (spin147 x) (prop claims-zero [(x Int)] (== (spin147 x) 0)))"));
  spinVerdict = r.reports?.[0]?.status ?? "no report";
  // Alive-after is the half that matters: a crash kills every LATER call, so a
  // verdict alone does not establish the runtime survived producing it.
  const after = JSON.parse(globalThis.oathCheck(snap.root,
    "(defn alive147 [] [(x Int)] Int (+ x x) (prop d [(x Int)] (== (alive147 x) (* 2 x))))"));
  spinAlive = after.reports?.[0]?.status === "accepted";
} catch (e) {
  spinVerdict = `THREW ${String(e).split("\n")[0]}`;
}
if (spinVerdict !== "falsified" || !spinAlive) {
  console.error("ERROR: non-terminating input did not falsify cleanly (#147 regression)");
  console.error(`  verdict: ${spinVerdict}   kernel alive afterwards: ${spinAlive}`);
  console.error("  The wasm depth guard must fire BEFORE the JS host stack is exhausted.");
  console.error("  maxEvalDepth for GOOS=js lives in oath/eval_depth_wasm.go and is bounded");
  console.error("  by the embedder's stack, not by Go memory — see #147 before raising it.");
  process.exit(1);
}

if (changed.length === 0 && unknown.length === 0 && errored.length === 0 && missing.length === 0) {
  console.log(`served wasm reproduces corpus identities ✓ (${reached.size}/${mustReach.length} required names from ${sources.length} files)`);
  console.log("  non-terminating input falsifies and the kernel survives ✓ (#147)");
  for (const [f, e] of EXCLUDED) console.log(`  EXCLUDED ${f} (${e.names.join(", ")}) — ${e.why}`);
  console.log("  NOTE: this is behavioural agreement on what the corpus exercises. It says NOTHING about whether the artifact is current.");
  process.exit(0);
}

console.error("ERROR: the served wasm disagrees with codebase/names.json");
for (const [label, rows, fmt] of [
  ["resolved to a DIFFERENT hash", changed, ([f, n, e, g]) => `${n} (${f}): expected ${e.slice(0, 12)}, wasm produced ${g.slice(0, 12)}`],
  ["produced a name the store does not record", unknown, ([f, n, h]) => `${n} (${f}) -> ${h.slice(0, 12)}`],
  ["threw while elaborating", errored, ([f, e]) => `${f}: ${e}`],
  ["recorded in names.json but never produced", missing.map((n) => [n]), ([n]) => n],
]) {
  if (!rows.length) continue;
  console.error(`  ${label}: ${rows.length}`);
  for (const r of rows.slice(0, 5)) console.error(`      ${fmt(r)}`);
  if (rows.length > 5) console.error(`      … and ${rows.length - 5} more`);
}
console.error("");
console.error("  A disagreement means the SERVED artifact computes different identities from");
console.error("  the reference kernel — a third-compilation divergence (build tags, the");
console.error("  syscall/js boundary), or a corpus/snapshot mismatch. It does NOT mean the");
console.error("  artifact is merely out of date; this gate cannot see that.");
process.exit(1);
