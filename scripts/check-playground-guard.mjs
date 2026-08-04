// Gate for the JS-side ADMIT boundary (#133). Runs with no build artifacts, so
// it can sit on every push next to the other website gates.
//
// The end-to-end witness lives in website/lib/playground/kernel.test.mjs and
// needs `make playground-assets` (a 16MB wasm build), so CI cannot run it on
// every push. That test SKIPS cleanly when the assets are absent — which would
// make it a gate that silently measures nothing. This file covers the three
// things that can regress without it, each derived from a different half of the
// claim rather than from a list of files:
//
//   1. the predicate is correct              — a vector table, every outcome asserted
//   2. every host installs it                — derived from "who boots the kernel"
//   3. the SHIPPED copy matches the source   — the browser runs public/pgrt/, not lib/
//
// (3) matters most and is the least obvious: public/pgrt/lossless.js is a copy,
// so without this check the node test could pass against a fixed lib/ file while
// the browser served a stale one. The gate would then be evidence about a file
// nobody executes.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { firstUnpairedSurrogate } from "../website/lib/playground/lossless.js";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
let fail = 0;
const expect = (label, cond, got) => {
  console.log(`${cond ? "ok  " : "FAIL"}  ${label}${cond ? "" : "  got: " + got}`);
  if (!cond) fail++;
};

// ---- 1. the predicate ------------------------------------------------------
// Both directions: strings that MUST be refused, and strings that MUST pass.
// A guard asserted only on the refusals is satisfiable by `return 0`.
const REFUSE = [
  ["lone high surrogate", "A\ud800B", 1],
  ["lone low surrogate", "A\udc00B", 1],
  ["high at end of string", "AB\ud800", 2],
  ["low before high (reversed pair)", "\udc00\ud800", 0],
  ["high followed by a second high", "\ud800𐀀", 0],
];
const ALLOW = [
  ["empty", ""],
  ["ascii", "(defn f [] [] Int 1)"],
  ["two-byte", "AÃB"],
  ["genuine U+FFFD", "A�B"],
  ["valid pair U+10000", "A\u{10000}B"],
  ["pair at end of string", "AB\u{10FFFF}"],
  ["adjacent pairs", "\u{10000}\u{10001}"],
];
for (const [label, s, at] of REFUSE) {
  expect(`refuses: ${label}`, firstUnpairedSurrogate(s) === at, String(firstUnpairedSurrogate(s)));
}
for (const [label, s] of ALLOW) {
  expect(`allows: ${label}`, firstUnpairedSurrogate(s) === -1, String(firstUnpairedSurrogate(s)));
}

// Cross-check against the encoder rather than against a second hand-written
// list: a string is losslessly encodable exactly when a UTF-8 round trip is the
// identity. This is the claim itself, so it catches a predicate that is
// self-consistently wrong in a way a fixed table would not.
const enc = new TextEncoder(), dec = new TextDecoder();
for (const [, s] of [...REFUSE, ...ALLOW]) {
  const roundTrips = dec.decode(enc.encode(s)) === s;
  expect(`round-trip agrees for ${JSON.stringify(s).slice(0, 24)}`, roundTrips === (firstUnpairedSurrogate(s) === -1), `roundTrips=${roundTrips}`);
}

// ---- 2. every host that boots the kernel installs the guard ----------------
// The universe is "files that call go.run(instance)", read off the tree — NOT a
// list of the three hosts that exist today. A fourth host added later is
// covered by this check on the day it is written.
const hosts = [];
const walk = (dir) => {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name === "node_modules" || e.name === ".next" || e.name.startsWith(".")) continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p);
    else if (/\.(js|mjs|ts|tsx)$/.test(e.name)) {
      const src = fs.readFileSync(p, "utf8");
      // lossless.js names `go.run(instance)` in an error message, so a bare
      // text match counts the guard's own definition as a host. Excluding the
      // definer keeps the reported list equal to the set actually claimed.
      // The match stays textual and therefore over-inclusive by design: a false
      // positive here fails the gate loudly, whereas a missed host would pass
      // it silently.
      if (/export function guardKernelExports/.test(src)) continue;
      if (/go\.run\s*\(\s*instance\s*\)/.test(src)) hosts.push([path.relative(ROOT, p), src]);
    }
  }
};
walk(path.join(ROOT, "website"));
expect("found at least one kernel host", hosts.length > 0, String(hosts.length));
for (const [rel, src] of hosts) {
  expect(`${rel} installs guardKernelExports`, /guardKernelExports\s*\(/.test(src), "no call");
}

// ---- 3. the shipped copy matches its source --------------------------------
for (const f of ["lossless.js", "memfs.js"]) {
  const a = path.join(ROOT, "website/lib/playground", f);
  const b = path.join(ROOT, "website/public/pgrt", f);
  const same = fs.existsSync(b) && fs.readFileSync(a, "utf8") === fs.readFileSync(b, "utf8");
  expect(`public/pgrt/${f} matches lib/playground/${f}`, same, "drifted — run 'make playground-assets'");
}

console.log(fail ? `\n${fail} FAILED` : "\nplayground ADMIT guard ✓");
process.exit(fail ? 1 : 0);
