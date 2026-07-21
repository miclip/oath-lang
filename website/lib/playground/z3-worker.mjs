// Z3 worker: runs z3-solver@4.16.0 (the corpus's exact solver version) async on
// its own thread, and answers the kernel worker synchronously via a
// SharedArrayBuffer + Atomics.notify. A fresh context per script because the
// kernel emits self-contained SMT-LIB (its own declarations each time).
//
// This is the Node (worker_threads) form used by bridge.test.mjs. The browser
// form is identical in shape: `self.onmessage` instead of parentPort, and the
// SharedArrayBuffer arrives via the initial message rather than workerData.
import { parentPort, workerData } from "node:worker_threads";
import { init } from "z3-solver";

const { flags, data } = workerData;
const F = new Int32Array(flags);
const D = new Uint8Array(data);
const enc = new TextEncoder();
const { Z3 } = await init();

parentPort.on("message", async (script) => {
  const cfg = Z3.mk_config();
  const ctx = Z3.mk_context(cfg);
  let out;
  try {
    out = String(await Z3.eval_smtlib2_string(ctx, script));
  } catch (e) {
    out = "ERROR " + (e?.message || e);
  }
  Z3.del_context(ctx);
  Z3.del_config(cfg);
  const b = enc.encode(out);
  D.set(b.subarray(0, D.length), 0);
  Atomics.store(F, 1, b.length);
  Atomics.store(F, 0, 1);
  Atomics.notify(F, 0);
});
parentPort.postMessage("ready");
