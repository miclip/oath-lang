// Drives the /try page in real headless Chromium: loads the wasm kernel in a
// worker, runs the gate on the examples, and — the point of Phase 2 (#34) —
// PROVES a novel definition by driving Z3 across the browser bridge. This is the
// browser-side counterpart to bridge.test.mjs: proof the whole integration
// (React + kernel.js worker + MemFS + wasm + z3-solver) works in an actual
// browser, not just Node.
//
// The prove path is CPU-bound (Z3 pthreads) and starves under the Next dev
// server's background compilation, so run this against a production build:
//   npm run build && npx next start -p 3211 &
//   BASE=http://localhost:3211 node lib/playground/browser.test.mjs
import { chromium } from "playwright";
const BASE = process.env.BASE || "http://localhost:3211";
const b = await chromium.launch();
const page = await b.newPage();
page.setDefaultTimeout(90000);
const errors = [];
page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
page.on("pageerror", (e) => errors.push(String(e)));

let fail = 0;
const ok = (label, cond, extra = "") => { console.log(`${cond ? "PASS" : "FAIL"}  ${label}${cond ? "" : "  " + extra}`); if (!cond) fail++; };

await page.goto(`${BASE}/try`, { waitUntil: "domcontentloaded" });
const clickExample = (name) => page.getByRole("button", { name }).click();
const clickVerify = () => page.evaluate(() => [...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "Verify")?.click());
const clickProve = () => page.evaluate(() => [...document.querySelectorAll("button")].find((b) => /Prove/.test(b.textContent))?.click());

// Kernel ready ⇒ Verify enabled.
await page.waitForFunction(() => {
  const v = [...document.querySelectorAll("button")].find((x) => x.textContent.trim() === "Verify");
  return v && !v.disabled;
});
ok("kernel loaded in browser (Verify enabled)", true);

// verify() clicks Verify and waits for a real status badge (not the static note),
// then returns the reports region text.
async function verify() {
  await clickVerify();
  await page.waitForFunction(() =>
    [...document.querySelectorAll("span")].some((s) => /^(accepted|falsified|tested)$/i.test(s.textContent.trim()))
  );
  await page.waitForTimeout(150);
  return page.evaluate(() => document.querySelector("main").innerText);
}

// 1) sort (default) → recorded PROVEN 7/7 via content-hash match.
let text = await verify();
ok("sort → recorded PROVEN 7/7", /PROVEN \(all 7/.test(text), text.slice(-300));

// 2) bad-reverse → FALSIFIED + counterexample.
await clickExample(/bad-reverse/);
text = await verify();
ok("bad-reverse → FALSIFIED + counterexample", /FALSIFIED/i.test(text) && /counterexample/i.test(text), text.slice(-300));

// 3) novel def → tested/accepted, then PROVEN live via the Z3 bridge.
await clickExample(/your own/);
text = await verify();
ok("novel def → accepted (tested)", /accepted/i.test(text), text.slice(-200));

// Prove: enabled once the Z3 bridge is up and the def wasn't refuted.
await page.waitForFunction(() => {
  const p = [...document.querySelectorAll("button")].find((x) => /Prove/.test(x.textContent));
  return p && !p.disabled;
});
await clickProve();
await page.waitForFunction(() =>
  [...document.querySelectorAll("pre")].some((e) => /proven: \d+\/\d+/.test(e.textContent || ""))
);
const proof = await page.evaluate(() => [...document.querySelectorAll("pre")].find((e) => /proven:/.test(e.textContent || ""))?.textContent || "");
ok("novel def → PROVEN live (2/2) via Z3 in the browser", /proven: 2\/2/.test(proof) && /∎ PROVEN/.test(proof), proof.slice(0, 200));

ok("no console/page errors", errors.length === 0, errors.slice(0, 3).join(" | "));
console.log(fail ? `\n${fail} FAILED` : "\nbrowser integration (verify + prove) works ✓");
await b.close();
process.exit(fail ? 1 : 0);
