"use client";

import { useEffect, useRef, useState } from "react";
import { Nav } from "@/components/Nav";
import { Footer } from "@/components/Footer";
// The real kernel, compiled to wasm, running in a worker. Nothing here is mocked.
import { init } from "@/lib/playground/kernel";

const EXAMPLES: { label: string; note: string; src: string }[] = [
  {
    label: "insertion sort",
    note: "matches the corpus → shows the recorded 7/7 PROVEN",
    src: `(defn sort [] [(xs (List Int))] (List Int)
  (match xs
    ((Nil) (Nil [Int]))
    ((Cons h t) (insert h (sort t))))
  (prop output-is-sorted [(xs (List Int))]
    (is-sorted (sort xs)))
  (prop preserves-length [(xs (List Int))]
    (== (length [Int] (sort xs)) (length [Int] xs)))
  (prop preserves-counts [(x Int) (xs (List Int))]
    (== (count x (sort xs)) (count x xs)))
  (prop snoc-is-insert [(x Int) (xs (List Int))]
    (== (sort (append [Int] xs (Cons [Int] x (Nil [Int]))))
        (insert x (sort xs))))
  (prop sorted-is-fixpoint [(xs (List Int))]
    (if (is-sorted xs) (== (sort xs) xs) true))
  (prop idempotent [(xs (List Int))]
    (== (sort (sort xs)) (sort xs)))
  (prop reverse-invariant [(xs (List Int))]
    (== (sort (reverse [Int] xs)) (sort xs))))`,
  },
  {
    label: "bad-reverse (honest exhibit)",
    note: "a wrong reverse → the kernel FALSIFIES it with a counterexample",
    src: `(defn bad-reverse [a] [(xs (List a))] (List a)
  (match xs
    ((Nil) (Nil [a]))
    ((Cons h t) (Cons [a] h (bad-reverse [a] t))))
  (prop involution [(xs (List Int))]
    (== (bad-reverse [Int] (bad-reverse [Int] xs)) xs))
  (prop antidistributes-over-append [(xs (List Int)) (ys (List Int))]
    (== (bad-reverse [Int] (append [Int] xs ys))
        (append [Int] (bad-reverse [Int] ys) (bad-reverse [Int] xs)))))`,
  },
  {
    label: "your own (novel → PROVE it)",
    note: "a novel def: typecheck + test live, then run Z3 in your browser to PROVE it",
    src: `(defn double [] [(x Int)] Int
  (+ x x)
  (prop is-double [(x Int)] (== (double x) (* 2 x)))
  (prop adds-to-itself [(x Int)] (== (double x) (+ x x))))`,
  },
  {
    label: "novel recursive (induction)",
    note: "a novel recursive def whose property Z3 proves by structural induction, live",
    src: `(defn cnt [] [(xs (List Int))] Int
  (match xs
    ((Nil) 0)
    ((Cons h t) (+ 1 (cnt t))))
  (prop nonneg [(xs (List Int))] (<= 0 (cnt xs)))
  (prop counts-cons [(x Int) (xs (List Int))]
    (== (cnt (Cons [Int] x xs)) (+ 1 (cnt xs)))))`,
  },
];

type Prop = { name: string; passed: number; failed: boolean; counterexample?: string };
type Report = {
  name: string;
  status: string;
  guarantee?: string;
  termination?: string;
  confinement?: string;
  error?: string;
  props?: Prop[];
};
type Result = { ok: boolean; error?: string; reports?: Report[]; notes?: string[] };
type ProofResult = { ok: boolean; proofReport?: string; error?: string };

type KernelAPI = {
  check: (s: string) => Promise<Result>;
  prove: (s: string, name: string) => Promise<ProofResult>;
  proveReady: boolean;
  whenProveReady: () => Promise<boolean>;
};

function badgeColor(status: string): string {
  if (status === "accepted") return "var(--sage-bright)";
  if (status === "falsified") return "var(--clay)";
  return "var(--cream-faint)";
}

// The defn names in a source, in order — what `prove` needs to name each goal.
function defnNames(src: string): string[] {
  return [...src.matchAll(/\(defn\s+([^\s()[\]]+)/g)].map((m) => m[1]);
}

export default function TryPage() {
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [errMsg, setErrMsg] = useState("");
  const [src, setSrc] = useState(EXAMPLES[0].src);
  const [result, setResult] = useState<Result | null>(null);
  const [running, setRunning] = useState(false);
  const [proveReady, setProveReady] = useState(false);
  const [proving, setProving] = useState(false);
  const [proof, setProof] = useState<string | null>(null);
  const [proofErr, setProofErr] = useState<string | null>(null);
  const api = useRef<KernelAPI | null>(null);

  useEffect(() => {
    let live = true;
    init()
      .then((a: KernelAPI) => {
        if (!live) return;
        api.current = a;
        setStatus("ready");
        a.whenProveReady().then((ok) => live && setProveReady(ok));
      })
      .catch((e: unknown) => {
        if (!live) return;
        setErrMsg(String(e));
        setStatus("error");
      });
    return () => {
      live = false;
    };
  }, []);

  async function run() {
    if (!api.current) return;
    setRunning(true);
    setProof(null);
    setProofErr(null);
    try {
      setResult(await api.current.check(src));
    } catch (e) {
      setResult({ ok: false, error: String(e) });
    }
    setRunning(false);
  }

  async function prove() {
    if (!api.current) return;
    const names = defnNames(src);
    if (names.length === 0) {
      setProofErr("no (defn …) found to prove");
      return;
    }
    setProving(true);
    setProof(null);
    setProofErr(null);
    try {
      const parts: string[] = [];
      for (const name of names) {
        const r = await api.current.prove(src, name);
        if (r.error) {
          setProofErr(r.error);
          setProving(false);
          return;
        }
        parts.push((r.proofReport || "").trimEnd());
      }
      setProof(parts.join("\n\n"));
    } catch (e) {
      setProofErr(String(e));
    }
    setProving(false);
  }

  // Prove is offered once the def has been checked and wasn't refuted, and the
  // Z3 bridge came up. A falsified def has nothing to prove.
  const canProve =
    proveReady &&
    !!result?.reports?.length &&
    !result.reports.some((r) => r.status === "falsified" || r.status === "rejected");

  return (
    <>
      <Nav />
      <main className="wrap" style={{ paddingTop: 40, paddingBottom: 80 }}>
        <p className="eyebrow">Live · runs in your browser</p>
        <h1 style={{ fontSize: "clamp(30px,4.5vw,46px)", marginBottom: 14 }}>Paste a definition. Watch the kernel judge it.</h1>
        <p className="section-lead" style={{ maxWidth: 680 }}>
          This is the actual Oath kernel compiled to WebAssembly — the same gate that runs on the
          command line. It typechecks your definition, runs every property on 200 deterministic
          cases, and reports an honest verdict. Then <strong>Prove</strong> runs the real Z3 solver,
          also in your browser, to prove a novel definition for <em>all</em> inputs — direct and by
          induction. Nothing here is mocked.{" "}
          {status === "loading" && <span style={{ color: "var(--cream-faint)" }}>Loading kernel…</span>}
          {status === "error" && <span style={{ color: "var(--clay)" }}>Kernel failed to load: {errMsg}</span>}
        </p>

        <div style={{ display: "flex", gap: 10, flexWrap: "wrap", margin: "22px 0 12px" }}>
          {EXAMPLES.map((ex) => (
            <button
              key={ex.label}
              onClick={() => {
                setSrc(ex.src);
                setResult(null);
                setProof(null);
                setProofErr(null);
              }}
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 12.5,
                padding: "7px 12px",
                border: "1px solid var(--line)",
                borderRadius: 7,
                background: src === ex.src ? "var(--sand)" : "transparent",
                color: "var(--cream)",
                cursor: "pointer",
              }}
              title={ex.note}
            >
              {ex.label}
            </button>
          ))}
        </div>

        <textarea
          value={src}
          onChange={(e) => setSrc(e.target.value)}
          spellCheck={false}
          style={{
            width: "100%",
            minHeight: 260,
            fontFamily: "var(--font-mono)",
            fontSize: 13,
            lineHeight: 1.55,
            padding: 16,
            background: "var(--ink)",
            color: "var(--cream)",
            border: "1px solid var(--line)",
            borderRadius: 10,
            resize: "vertical",
          }}
        />

        <div style={{ marginTop: 14, display: "flex", gap: 12, alignItems: "center", flexWrap: "wrap" }}>
          <button
            onClick={run}
            disabled={status !== "ready" || running || proving}
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 14,
              fontWeight: 600,
              padding: "10px 22px",
              border: "none",
              borderRadius: 8,
              background: status === "ready" ? "var(--sage)" : "var(--line)",
              color: "var(--ink)",
              cursor: status === "ready" && !running ? "pointer" : "default",
            }}
          >
            {running ? "verifying…" : "Verify"}
          </button>

          <button
            onClick={prove}
            disabled={!canProve || proving || running}
            title={
              proveReady
                ? "Run Z3 in your browser to prove this for all inputs"
                : "Verify a definition first; the Z3 bridge proves it for all inputs"
            }
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 14,
              fontWeight: 600,
              padding: "10px 22px",
              border: "1px solid var(--sage)",
              borderRadius: 8,
              background: canProve && !proving ? "transparent" : "var(--line)",
              color: canProve && !proving ? "var(--sage-bright)" : "var(--cream-faint)",
              cursor: canProve && !proving ? "pointer" : "default",
            }}
          >
            {proving ? "proving with Z3…" : "Prove (Z3)"}
          </button>

          {status === "ready" && !proveReady && (
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11.5, color: "var(--cream-faint)" }}>
              Z3 bridge starting…
            </span>
          )}
        </div>

        <p style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--cream-faint)", lineHeight: 1.65, marginTop: 14, maxWidth: 680 }}>
          <strong style={{ color: "var(--cream-dim)" }}>Verify</strong> is the gate — typecheck, 200
          deterministic hash-seeded cases per property, and the termination/confinement analyses —
          reported as an honest <code>tested</code>. Paste a corpus definition verbatim (like the
          sort example) and its content hash matches, surfacing that definition&apos;s recorded{" "}
          <code>PROVEN</code>. <strong style={{ color: "var(--cream-dim)" }}>Prove</strong> runs the
          real prover — direct, structural induction, the lemma fixpoint — discharging every
          obligation to Z3 (the same <code>4.16.0</code> the corpus used) compiled to WebAssembly and
          running in a worker beside the kernel. A proof is valid modulo int64 overflow, and the
          report says so.
        </p>

        {proof && (
          <pre
            style={{
              marginTop: 22,
              fontFamily: "var(--font-mono)",
              fontSize: 13,
              color: "var(--sage-bright)",
              background: "var(--ink)",
              border: "1px solid var(--sage)",
              borderRadius: 10,
              padding: 18,
              whiteSpace: "pre-wrap",
              overflowX: "auto",
            }}
          >
            {proof}
          </pre>
        )}
        {proofErr && (
          <pre
            style={{
              marginTop: 22,
              fontFamily: "var(--font-mono)",
              fontSize: 13,
              color: "var(--clay)",
              background: "var(--ink)",
              border: "1px solid var(--line)",
              borderRadius: 10,
              padding: 18,
              whiteSpace: "pre-wrap",
            }}
          >
            {proofErr}
          </pre>
        )}

        {result && (
          <div style={{ marginTop: 28 }}>
            {result.error && !result.reports?.length && (
              <pre
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: 13,
                  color: "var(--clay)",
                  background: "var(--ink)",
                  border: "1px solid var(--line)",
                  borderRadius: 8,
                  padding: 16,
                  whiteSpace: "pre-wrap",
                }}
              >
                {result.error}
              </pre>
            )}
            {result.reports?.map((r, i) => (
              <div
                key={i}
                style={{
                  border: "1px solid var(--line)",
                  borderRadius: 10,
                  padding: 18,
                  marginBottom: 14,
                  background: "var(--ink)",
                }}
              >
                <div style={{ display: "flex", alignItems: "baseline", gap: 12, flexWrap: "wrap" }}>
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 16, fontWeight: 600 }}>{r.name}</span>
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 11,
                      textTransform: "uppercase",
                      letterSpacing: 0.5,
                      padding: "3px 9px",
                      borderRadius: 5,
                      color: "var(--ink)",
                      background: badgeColor(r.status),
                    }}
                  >
                    {r.status}
                  </span>
                  {r.guarantee && (
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 12.5, color: "var(--cream-dim)" }}>{r.guarantee}</span>
                  )}
                  {r.termination && (
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--cream-faint)" }}>· {r.termination}</span>
                  )}
                </div>
                {r.error && (
                  <pre style={{ fontFamily: "var(--font-mono)", fontSize: 12.5, color: "var(--clay)", marginTop: 10, whiteSpace: "pre-wrap" }}>{r.error}</pre>
                )}
                {r.props && r.props.length > 0 && (
                  <ul style={{ listStyle: "none", padding: 0, margin: "12px 0 0" }}>
                    {r.props.map((p, j) => (
                      <li key={j} style={{ fontFamily: "var(--font-mono)", fontSize: 12.5, padding: "3px 0", color: "var(--cream-dim)" }}>
                        <span style={{ color: p.failed ? "var(--clay)" : "var(--sage-bright)" }}>{p.failed ? "✗" : "✓"}</span>{" "}
                        {p.name}
                        {p.failed && p.counterexample && (
                          <span style={{ color: "var(--clay)" }}> — counterexample: {p.counterexample}</span>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            ))}
          </div>
        )}
      </main>
      <Footer />
    </>
  );
}
