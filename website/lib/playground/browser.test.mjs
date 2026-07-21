// Drives the /try page in real headless Chromium: loads the wasm kernel on the
// page, runs the gate on three pastes, asserts the rendered verdicts. This is
// the browser-side counterpart to bridge.test.mjs — proof the integration
// (React + kernel.js + MemFS + wasm) works in an actual browser, not just Node.
import { chromium } from "playwright";
const BASE = process.env.BASE || "http://localhost:3210";
const b = await chromium.launch();
const page = await b.newPage();
const errors = [];
page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
page.on("pageerror", (e) => errors.push(String(e)));

let fail = 0;
const ok = (label, cond, extra = "") => { console.log(`${cond ? "PASS" : "FAIL"}  ${label}${cond ? "" : "  " + extra}`); if (!cond) fail++; };

await page.goto(`${BASE}/try`, { waitUntil: "networkidle" });
// Kernel ready ⇒ Verify button enabled.
await page.getByRole("button", { name: "Verify" }).waitFor();
await page.waitForFunction(() => {
  const btns = [...document.querySelectorAll("button")];
  const v = btns.find((x) => x.textContent.trim() === "Verify");
  return v && !v.disabled;
}, { timeout: 30000 });
ok("kernel loaded in browser (Verify enabled)", true);

async function verify() {
  await page.getByRole("button", { name: "Verify" }).click();
  await page.waitForFunction(() => document.body.innerText.match(/PROVEN|FALSIFIED|tested|rejected|error/i), { timeout: 30000 });
  await page.waitForTimeout(200);
  return page.evaluate(() => document.querySelector("main").innerText);
}

// 1) sort (default) → recorded PROVEN 7/7
let text = await verify();
ok("sort → PROVEN 7/7", /PROVEN \(all 7/.test(text), text.slice(0, 300));

// 2) bad-reverse → FALSIFIED + counterexample
await page.getByRole("button", { name: /bad-reverse/ }).click();
text = await verify();
ok("bad-reverse → FALSIFIED + counterexample", /FALSIFIED/i.test(text) && /counterexample/i.test(text), text.slice(0, 300));

// 3) novel def → tested
await page.getByRole("button", { name: /your own/ }).click();
text = await verify();
ok("novel def → tested", /tested/i.test(text), text.slice(0, 300));

ok("no console/page errors", errors.length === 0, errors.slice(0, 3).join(" | "));
console.log(fail ? `\n${fail} FAILED` : "\nbrowser integration works ✓");
await b.close();
process.exit(fail ? 1 : 0);
