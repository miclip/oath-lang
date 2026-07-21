"use client";

import { useEffect, useRef, useState } from "react";
import { Nav } from "@/components/Nav";
import { Footer } from "@/components/Footer";
// The real kernel, compiled to wasm. Nothing here is mocked.
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
    label: "your own (a novel def)",
    note: "novel input is typechecked + tested live (200 cases); proof needs the Z3 upgrade",
    src: `(defn double [] [(x Int)] Int
  (+ x x)
  (prop is-double [(x Int)] (== (double x) (* 2 x)))
  (prop adds-to-itself [(x Int)] (== (double x) (+ x x))))`,
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

function badgeColor(status: string): string {
  if (status === "accepted") return "var(--sage-bright)";
  if (status === "falsified") return "var(--clay)";
  return "var(--cream-faint)";
}

export default function TryPage() {
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [errMsg, setErrMsg] = useState("");
  const [src, setSrc] = useState(EXAMPLES[0].src);
  const [result, setResult] = useState<Result | null>(null);
  const [running, setRunning] = useState(false);
  const check = useRef<((s: string) => Result) | null>(null);

  useEffect(() => {
    let live = true;
    init()
      .then((api: { check: (s: string) => Result }) => {
        if (!live) return;
        check.current = api.check;
        setStatus("ready");
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

  function run() {
    if (!check.current) return;
    setRunning(true);
    // Yield so the "verifying…" state paints before the (synchronous) wasm run.
    setTimeout(() => {
      try {
        setResult(check.current!(src));
      } catch (e) {
        setResult({ ok: false, error: String(e) });
      }
      setRunning(false);
    }, 0);
  }

  return (
    <>
      <Nav />
      <main className="wrap" style={{ paddingTop: 40, paddingBottom: 80 }}>
        <p className="eyebrow">Live · runs in your browser</p>
        <h1 style={{ fontSize: "clamp(30px,4.5vw,46px)", marginBottom: 14 }}>Paste a definition. Watch the kernel judge it.</h1>
        <p className="section-lead" style={{ maxWidth: 680 }}>
          This is the actual Oath kernel compiled to WebAssembly — the same gate that runs on the
          command line. It typechecks your definition, runs every property on 200 deterministic
          cases, and reports an honest verdict. A definition whose content hash matches the committed
          corpus shows that corpus definition&apos;s recorded Z3 proof.{" "}
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

        <div style={{ marginTop: 14 }}>
          <button
            onClick={run}
            disabled={status !== "ready" || running}
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
        </div>

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
            {result.notes?.map((n, i) => (
              <p key={i} style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--cream-faint)", lineHeight: 1.6, marginTop: 8 }}>
                {n}
              </p>
            ))}
          </div>
        )}
      </main>
      <Footer />
    </>
  );
}
