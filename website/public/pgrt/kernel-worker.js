// Kernel worker: hosts the real Oath kernel (wasm) plus the in-memory corpus,
// and wires globalThis.oathZ3 to the Z3 worker over a SharedArrayBuffer bridge.
// The kernel MUST run in a worker because oathZ3 blocks on Atomics.wait, which a
// browser main thread cannot do. Its synchronous prover then discharges every
// z3 obligation across the bridge with no change to the prover itself.
//
// This is the browser (module worker) form of lib/playground/kernel-worker.mjs,
// which the Node bridge test exercises with identical wasm, MemFS, and z3-solver.
import { MemFS } from "./memfs.js";

// Minimal browser shim for the two globals Go's js/wasm fs layer expects
// (same as the main-thread loader in kernel.js).
function pathShim() {
  const resolve = (...parts) => {
    const p = parts.filter(Boolean).join("/");
    const abs = p.startsWith("/");
    const out = [];
    for (const seg of p.split("/")) {
      if (seg === "" || seg === ".") continue;
      if (seg === "..") out.pop();
      else out.push(seg);
    }
    return (abs ? "/" : "") + out.join("/");
  };
  return { resolve };
}

function b64ToU8(b64) {
  const bin = atob(b64);
  const u8 = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) u8[i] = bin.charCodeAt(i);
  return u8;
}

let root = null;
let proveReady = false;
let bridgePoisoned = false;

// Stage 1: the kernel itself — corpus + wasm. This never needs the z3 bridge,
// so `check` works even if cross-origin isolation (and thus SharedArrayBuffer)
// is unavailable.
async function bootKernel() {
  const snap = await (await fetch("/pgrt/corpus-snapshot.json")).json();
  root = snap.root;
  const files = { [root]: "DIR", [root + "/objects"]: "DIR", [root + "/meta"]: "DIR" };
  for (const [p, b64] of Object.entries(snap.files)) files[p] = b64ToU8(b64);
  globalThis.fs = new MemFS(files);
  globalThis.path = pathShim();

  const execSrc = await (await fetch("/pgrt/wasm_exec.js")).text();
  (0, eval)(execSrc); // defines globalThis.Go
  const go = new globalThis.Go();
  const bytes = await (await fetch("/pgrt/oath.wasm")).arrayBuffer();
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(instance); // init() registers oathCheck/oathProve; main() parks
}

// Stage 2: the z3 bridge — spawn the Z3 worker and expose globalThis.oathZ3 as a
// blocking call. If this fails (no cross-origin isolation, no SharedArrayBuffer),
// oathZ3 stays absent; z3host_wasm's z3Available() then reports z3 unavailable
// and prove degrades gracefully while check keeps working.
async function bootBridge() {
  const flags = new SharedArrayBuffer(8);
  const data = new SharedArrayBuffer(256 * 1024);
  const F = new Int32Array(flags);
  const D = new Uint8Array(data);
  const dec = new TextDecoder();
  const z3 = new Worker("/pgrt/z3/z3-worker.js"); // classic worker (importScripts)
  await new Promise((res, rej) => {
    z3.onerror = (e) => rej(new Error("z3 worker error: " + (e.message || e)));
    z3.onmessage = (e) => { if (e.data === "ready") res(); };
    z3.postMessage({ flags, data });
  });
  // A z3 query normally self-limits via its (:rlimit …) and returns in
  // milliseconds. This wall cap is a safety net: if a query ever wedges (a
  // pathological paste, a pthread stall), we don't block the page forever. On a
  // timeout the buffer can no longer be trusted — a late notify would desync the
  // next call — so we poison the bridge and return "" (which execZ3 reads as an
  // invalid attempt, never a false verdict). A reload gives a fresh bridge.
  const Z3_WALL_MS = 30_000;
  globalThis.oathZ3 = (script) => {
    if (bridgePoisoned) return "";
    Atomics.store(F, 0, 0);
    z3.postMessage(script);
    if (Atomics.wait(F, 0, 0, Z3_WALL_MS) === "timed-out") {
      bridgePoisoned = true;
      return "";
    }
    // Copy out of the SharedArrayBuffer first: the browser's TextDecoder refuses
    // to decode a view backed by shared memory.
    return dec.decode(new Uint8Array(D.subarray(0, Atomics.load(F, 1))));
  };
  proveReady = true;
}

// Two separate boot promises: checking needs only the kernel, so a `check`
// never waits behind the 32MB Z3 download. Proving waits for the bridge too.
const kernelBooted = bootKernel().then(() => self.postMessage({ type: "kernel-ready" }));
const bridgeBooted = kernelBooted.then(async () => {
  try {
    await bootBridge();
    self.postMessage({ type: "bridge-ready" });
  } catch (e) {
    self.postMessage({ type: "bridge-failed", error: String(e?.message || e) });
  }
});

self.onmessage = async (e) => {
  const { id, op, source, name } = e.data || {};
  try {
    if (op === "check") {
      await kernelBooted;
      const checked = JSON.parse(globalThis.oathCheck(root, String(source)));
      self.postMessage({ id, checked });
    } else if (op === "prove") {
      await bridgeBooted;
      if (!proveReady) {
        self.postMessage({ id, error: "z3 bridge unavailable (cross-origin isolation required)" });
        return;
      }
      // Re-put the CURRENT source and confirm the named def actually passed the
      // gate this time — otherwise a rejected paste whose name collides with a
      // previously stored def would prove the stale stored def, not the code in
      // the editor. Only prove what the current source accepted.
      const checked = JSON.parse(globalThis.oathCheck(root, String(source)));
      const rep = (checked.reports || []).find((r) => r.name === name);
      if (!rep || rep.status !== "accepted") {
        self.postMessage({ id, error: "definition did not pass the gate — nothing to prove" });
        return;
      }
      const proof = JSON.parse(globalThis.oathProve(root, String(name)));
      self.postMessage({ id, proof });
    }
  } catch (err) {
    self.postMessage({ id, error: String(err?.message || err) });
  }
};
