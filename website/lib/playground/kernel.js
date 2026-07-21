// Browser loader for the real Oath kernel compiled to wasm (#34). The kernel and
// the committed corpus live in a Web Worker (kernel-worker.js) so its
// synchronous prover can block on Atomics.wait while a second worker runs Z3 —
// a browser main thread cannot Atomics.wait. This module is the main-thread
// handle: it spawns the worker and exposes async check() and prove().
//
// check() runs the ACTUAL gate — parse, elaborate, typecheck, deterministic
// verification, termination and confinement analyses — the same code paths as
// `oath put`. prove() runs the REAL prover (direct + induction + lemma fixpoint)
// with every Z3 obligation discharged across the bridge to z3-solver@4.16.0.
// Nothing is mocked. A paste whose content hash matches a corpus definition
// reports that definition's RECORDED verdict — content addressing at work.

let ready = null;

// init() spawns the kernel worker once and resolves to a { check, prove,
// proveReady, whenProveReady } API. Idempotent.
export function init({ workerURL = "/pgrt/kernel-worker.js" } = {}) {
  if (ready) return ready;
  ready = (async () => {
    const worker = new Worker(workerURL, { type: "module" });
    const pending = new Map();
    let nextId = 1;
    let proveReady = false;
    let proveResolve;
    const proveReadyPromise = new Promise((r) => (proveResolve = r));

    await new Promise((resolve, reject) => {
      worker.onmessage = (e) => {
        const m = e.data;
        if (m.type === "kernel-ready") { resolve(); return; }
        if (m.type === "bridge-ready") { proveReady = true; proveResolve(true); return; }
        if (m.type === "bridge-failed") { proveReady = false; proveResolve(false); return; }
        // Ordinary request/response, keyed by id.
        const p = pending.get(m.id);
        if (!p) return;
        pending.delete(m.id);
        if (m.error) p.reject(new Error(m.error));
        else p.resolve(m.checked ?? m.proof);
      };
      worker.onerror = (e) => reject(new Error("kernel worker failed: " + (e.message || e)));
    });

    const call = (op, payload) =>
      new Promise((resolve, reject) => {
        const id = nextId++;
        pending.set(id, { resolve, reject });
        worker.postMessage({ id, op, ...payload });
      });

    return {
      check: (source) => call("check", { source: String(source) }),
      prove: (source, name) => call("prove", { source: String(source), name: String(name) }),
      get proveReady() { return proveReady; },
      whenProveReady: () => proveReadyPromise,
    };
  })();
  return ready;
}
