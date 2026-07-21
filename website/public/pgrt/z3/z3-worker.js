// Z3 worker: runs z3-solver@4.16.0 (the corpus's exact solver version) in the
// browser and answers the kernel worker SYNCHRONOUSLY via a SharedArrayBuffer +
// Atomics.notify. The kernel's prover is synchronous; it blocks on Atomics.wait
// while this thread evaluates the SMT-LIB async, so the prover discharges each
// obligation across the bridge with no change to the prover itself.
//
// This file lives IN /pgrt/z3/ so Emscripten's script-directory (the worker's
// own URL) is the dir holding z3-built.wasm and the pthread sub-workers.
self.global = self; // z3-solver's browser build reads global.initZ3 (a Node-ism)
importScripts("z3-built.js");  // relative → sets self.initZ3, base = /pgrt/z3/
importScripts("z3-solver.js"); // relative → sets self.__z3solverInit

// z3-solver calls initZ3() with NO config, but Emscripten (loaded via
// importScripts) can't determine its own URL — so it would locate the wasm and
// spawn its pthread workers against `undefined`. Wrap the factory to inject both
// paths explicitly. This is the seam z3-solver leaves for us.
const realInit = self.initZ3;
self.initZ3 = (opts = {}) =>
  realInit({
    locateFile: (f) => "/pgrt/z3/" + f,
    mainScriptUrlOrBlob: "/pgrt/z3/z3-built.js",
    ...opts,
  });

const enc = new TextEncoder();
let Z3, F, D;
const zReady = self.__z3solverInit().then((r) => { Z3 = r.Z3; });

self.onmessage = async (e) => {
  // Handshake: the kernel worker hands us the shared flag + data buffers.
  if (e.data && e.data.flags) {
    F = new Int32Array(e.data.flags);
    D = new Uint8Array(e.data.data);
    await zReady;
    self.postMessage("ready");
    return;
  }
  // Otherwise: an SMT-LIB script to evaluate. A fresh context per script because
  // the kernel emits self-contained SMT-LIB (its own declarations each time).
  const script = e.data;
  await zReady;
  const cfg = Z3.mk_config();
  const ctx = Z3.mk_context(cfg);
  let out;
  try {
    out = String(await Z3.eval_smtlib2_string(ctx, script));
  } catch (err) {
    out = "ERROR " + (err?.message || err);
  }
  Z3.del_context(ctx);
  Z3.del_config(cfg);
  const b = enc.encode(out);
  D.set(b.subarray(0, D.length), 0);
  Atomics.store(F, 1, b.length);
  Atomics.store(F, 0, 1);
  Atomics.notify(F, 0);
};
