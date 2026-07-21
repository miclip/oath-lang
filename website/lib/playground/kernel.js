// Browser loader for the real Oath kernel compiled to wasm (#34). It mounts the
// committed corpus into an in-memory filesystem and runs the ACTUAL gate —
// parse, elaborate, typecheck, deterministic verification, termination and
// confinement analyses — the same code paths as `oath put`. Nothing is mocked.
//
// PROOF is not available in the browser (it needs z3 as a subprocess): a novel
// definition reports `tested`, never `proven`. But a paste whose content hash
// matches a corpus definition reports that definition's RECORDED verdict —
// content addressing doing exactly what it is for, so `sort` pasted verbatim
// shows the real 7/7 PROVEN the corpus carries.
import { MemFS } from "./memfs.js";

// Minimal browser shim for the two globals Go's js/wasm fs layer expects.
function pathShim() {
  const resolve = (...parts) => {
    let p = parts.filter(Boolean).join("/");
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

let ready = null;

// init() loads once and resolves to a { check } API. Idempotent.
export function init({ wasmURL = "/pgrt/oath.wasm", snapshotURL = "/pgrt/corpus-snapshot.json", execURL = "/pgrt/wasm_exec.js" } = {}) {
  if (ready) return ready;
  ready = (async () => {
    const snap = await (await fetch(snapshotURL)).json();
    const files = { [snap.root]: "DIR", [snap.root + "/objects"]: "DIR", [snap.root + "/meta"]: "DIR" };
    for (const [p, b64] of Object.entries(snap.files)) {
      const bin = atob(b64);
      const u8 = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) u8[i] = bin.charCodeAt(i);
      files[p] = u8;
    }
    globalThis.fs = new MemFS(files);
    globalThis.path = pathShim();

    // Go's wasm_exec.js defines globalThis.Go. Load it once.
    if (!globalThis.Go) {
      await new Promise((res, rej) => {
        const s = document.createElement("script");
        s.src = execURL; s.onload = res; s.onerror = rej;
        document.head.appendChild(s);
      });
    }
    const go = new globalThis.Go();
    // instantiateStreaming needs application/wasm; fall back to arrayBuffer if
    // the host serves the wrong MIME type.
    let instance;
    try {
      ({ instance } = await WebAssembly.instantiateStreaming(fetch(wasmURL), go.importObject));
    } catch {
      const bytes = await (await fetch(wasmURL)).arrayBuffer();
      ({ instance } = await WebAssembly.instantiate(bytes, go.importObject));
    }
    go.run(instance); // init() registers oathCheck; main() parks
    return {
      root: snap.root,
      check: (source) => JSON.parse(globalThis.oathCheck(snap.root, String(source))),
      kernelVersion: globalThis.oathKernelVersion,
    };
  })();
  return ready;
}
