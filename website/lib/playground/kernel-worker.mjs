// Kernel worker: hosts the Oath kernel wasm and the in-memory corpus, and wires
// globalThis.oathZ3 to the Z3 worker. Because oathZ3 blocks on Atomics.wait,
// the kernel MUST run in a worker (a browser main thread cannot Atomics.wait) —
// the kernel's synchronous prover then discharges each z3 obligation across the
// bridge without any change to the prover itself.
//
// Node (worker_threads) form used by bridge.test.mjs; the browser form differs
// only in the Worker/global plumbing.
import { parentPort, Worker } from "node:worker_threads";
import { MemFS } from "./memfs.js";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PUB = path.join(HERE, "../../public");

const snap = JSON.parse(fs.readFileSync(path.join(PUB, "corpus-snapshot.json"), "utf8"));
const files = { [snap.root]: "DIR", [snap.root + "/objects"]: "DIR", [snap.root + "/meta"]: "DIR" };
for (const [p, b64] of Object.entries(snap.files)) files[p] = new Uint8Array(Buffer.from(b64, "base64"));
globalThis.fs = new MemFS(files);
globalThis.path = path;

// The proven bridge.
const flags = new SharedArrayBuffer(8);
const data = new SharedArrayBuffer(256 * 1024);
const F = new Int32Array(flags);
const D = new Uint8Array(data);
const dec = new TextDecoder();
const z3 = new Worker(new URL("./z3-worker.mjs", import.meta.url), { workerData: { flags, data } });
await new Promise((r) => z3.once("message", r));
globalThis.oathZ3 = (script) => {
  Atomics.store(F, 0, 0);
  z3.postMessage(script);
  Atomics.wait(F, 0, 0);
  return dec.decode(D.subarray(0, Atomics.load(F, 1)));
};

const execSrc = fs.readFileSync(path.join(PUB, "wasm_exec.js"), "utf8");
(0, eval)(execSrc);
const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(fs.readFileSync(path.join(PUB, "oath.wasm")), go.importObject);
go.run(instance);

parentPort.on("message", ({ source, name }) => {
  const checked = JSON.parse(globalThis.oathCheck(snap.root, source));
  const proof = name ? JSON.parse(globalThis.oathProve(snap.root, name)) : null;
  parentPort.postMessage({ checked, proof });
});
parentPort.postMessage("kernel-ready");
