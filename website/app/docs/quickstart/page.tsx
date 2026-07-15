import type { Metadata } from "next";
import { CodeBlock } from "@/components/CodeBlock";

export const metadata: Metadata = {
  title: "Docs — Quickstart",
  description: "Build the Oath Language kernel and put your first verified definition.",
};

function Sh({ code }: { code: string }) {
  return (
    <div className="code">
      <div className="code-head">
        <span>shell</span>
      </div>
      <pre>
        <code style={{ color: "var(--cream)" }}>{code}</code>
      </pre>
    </div>
  );
}

export default function Quickstart() {
  return (
    <>
      <h1>Quickstart</h1>
      <p className="lead">
        The kernel is dependency-free Go. You need Go ≥ 1.25 and{" "}
        <code>z3</code> on your PATH (<code>brew install z3</code>) for proofs.
      </p>

      <h2>Build</h2>
      <Sh code={`git clone https://github.com/miclip/oath-lang
cd oath-lang
cd oath && go build -o oath . && cd ..`} />

      <h2>Put your first definition</h2>
      <p>
        <code>put</code> elaborates the surface syntax to the canonical AST,
        typechecks it, stores the object, and runs every property before the name
        is trusted.
      </p>
      <Sh code={`./oath/oath put examples/list.oath   # elaborate → typecheck → store → verify
./oath/oath ls                       # names, hashes, guarantees
./oath/oath get reverse              # human projection of a definition
./oath/oath eval '(reverse [Int] (Cons [Int] 1 (Cons [Int] 2 (Nil [Int]))))'`} />

      <h2>Watch a wrong definition get caught</h2>
      <p>
        <code>bad_reverse</code> returns its input unchanged. Its{" "}
        <code>involution</code> property passes — a lesson about weak specs — but the
        append law falsifies it, and the kernel prints a counterexample and records
        the definition as FALSIFIED.
      </p>
      <Sh code={`./oath/oath put examples/bad_reverse.oath`} />

      <h2>Prove it for all inputs</h2>
      <p>
        <code>prove</code> translates properties to SMT-LIB and asks Z3 to hold them
        for every input — including recursive functions, by structural induction.
        Proven properties become a lemma library for later proofs.
      </p>
      <Sh code={`./oath/oath put examples/sort.oath
./oath/oath prove sort               # discharge insertion sort's correctness to Z3`} />

      <h2>The agent interface</h2>
      <p>
        Three verbs turn the store into what an AI author actually consumes —
        queries and transactions instead of files.
      </p>
      <Sh code={`./oath/oath context sort append --budget 500   # spec-only slice: signatures,
                                               # props, guarantees — never bodies
./oath/oath put --json examples/sort.oath      # machine-readable verdicts
./oath/oath dependents append                  # reverse dependency query
./oath/oath mutate length                      # spec strength: do the properties
                                               # notice mutations of the body?`} />

      <div className="callout">
        <p>
          The MCP server (<code>oath serve</code>) exposes all of this over stdio, so
          any agent session can mount the substrate as native tools. The repo&apos;s{" "}
          <code>.mcp.json</code> registers it for Claude Code automatically.
        </p>
      </div>

      <h2>The surface syntax</h2>
      <p>
        Everything is explicitly annotated — type arguments included. Annotations are
        cheap for a machine author, and they keep the kernel free of inference:
        checking is pure structural synthesis.
      </p>
      <CodeBlock
        label="a full definition"
        code={`(defn length [a] [(xs (List a))] Int
  (match xs
    ((Nil) 0)
    ((Cons h t) (+ 1 (length [a] t))))
  (prop non-negative [(xs (List Int))]
    (<= 0 (length [Int] xs))))`}
      />
    </>
  );
}
