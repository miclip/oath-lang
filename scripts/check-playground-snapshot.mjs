// The playground's corpus snapshot must be the store it claims to mirror (#145).
//
// `make playground-assets` says "Regenerate after any kernel or corpus change",
// and nothing enforced it, so the served snapshot sat at 8bcc1bb (2026-07-23)
// while `codebase/` moved on: 82 definitions missing, 11 name bindings pointing
// at superseded objects. `/try` answered "what is this definition's hash?" with
// an older answer than the store's, on the page that exists to demonstrate that
// the hash IS the identity.
//
// Every other copied web asset already had a drift gate — check-web-docs,
// check-web-tutorials, check-web-ledger, check-playground-guard. This was the
// one derived asset without one, and the only one a human would never notice
// drifting, because a stale corpus still WORKS.
//
// The comparison CALLS THE GENERATOR rather than reimplementing it. A
// hand-written checker compares the fields its author remembered — names.json,
// almost certainly — and silently accepts drift in objects, meta or the journal.
import fs from "node:fs";
import path from "node:path";
import { execSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { buildSnapshot, SNAPSHOT_PATH } from "../website/lib/playground/gen-snapshot.mjs";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const dest = path.join(ROOT, SNAPSHOT_PATH);

if (!fs.existsSync(dest)) {
  console.error(`ERROR: ${SNAPSHOT_PATH} is missing — run 'make playground-assets'`);
  process.exit(1);
}

const want = JSON.stringify(buildSnapshot(ROOT));
const have = fs.readFileSync(dest, "utf8");
if (want === have) {
  const n = Object.keys(JSON.parse(have).files).length;
  console.log(`playground corpus snapshot matches codebase/ ✓ (${n} files)`);
  process.exit(0);
}

// Report WHAT drifted, not just that something did: a gate whose whole output is
// "regenerate" trains people to regenerate without looking, which is how a
// commit that also rewrites the journal gets waved through.
const a = JSON.parse(have).files, b = JSON.parse(want).files;
const missing = Object.keys(b).filter((k) => !(k in a));
const extra = Object.keys(a).filter((k) => !(k in b));
const changed = Object.keys(b).filter((k) => k in a && a[k] !== b[k]);
// Each example is printed UNDER ITS OWN LABEL. An unlabelled flat list gets
// misread — the first draft of this file printed all three groups together and
// its own author attributed missing objects to the "different bytes" group,
// briefly believing the content-addressed store had an integrity violation.
console.error(`ERROR: ${SNAPSHOT_PATH} has drifted from codebase/`);
for (const [label, keys] of [
  ["in codebase/ but not the snapshot", missing],
  ["in the snapshot but not codebase/", extra],
  ["present in both, different bytes ", changed],
]) {
  console.error(`  ${label}: ${keys.length}`);
  for (const k of keys.slice(0, 3)) console.error(`      ${k}`);
  if (keys.length > 3) console.error(`      … and ${keys.length - 3} more`);
}
// A dirty codebase/ makes this check report drift it did not cause, and the
// most likely dirtier is `make verify`, which re-puts every example and
// journals each one `accepted` even when no hash moves. Name that explicitly:
// otherwise the failure looks exactly like a stale snapshot, and the obvious
// response — regenerate — would bake a working-tree journal into the committed
// asset. This is the same hazard CLAUDE.md documents for `git checkout`.
try {
  const dirty = execSync("git status --porcelain -- codebase/", { cwd: ROOT, encoding: "utf8" }).trim();
  if (dirty) {
    console.error("");
    console.error("  NOTE: codebase/ is DIRTY in this working tree:");
    for (const l of dirty.split("\n").slice(0, 5)) console.error(`      ${l}`);
    console.error("  Some or all of the drift above may be uncommitted local change, not a");
    console.error("  stale snapshot. `make verify` appends to codebase/log.jsonl even when");
    console.error("  nothing moves. Check `git diff HEAD -- codebase/` BEFORE regenerating.");
  }
} catch {
  // not a git checkout, or git unavailable: the drift report above still stands
}
console.error(`  run 'make playground-assets' and commit the result`);
process.exit(1);
