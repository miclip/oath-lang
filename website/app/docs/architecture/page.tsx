import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Docs — Architecture",
  description:
    "How Oath Language fits together: the object store, the trusted gate, the prover, the two kernels, and the hosted team store.",
};

export default function Architecture() {
  return (
    <>
      <h1>Architecture</h1>
      <p className="lead">
        Oath Language is a small, auditable kernel around an immutable object store.
        Every
        piece exists to make one guarantee: an accepted name points at a definition
        that has been checked, not merely stored.
      </p>

      <h2>The object store</h2>
      <p>
        The store is a content-addressed object database. Each object&apos;s file name
        is the SHA-256 of its canonical binary encoding (the &quot;O1&quot; encoding —
        identity leaves the host language, so two kernels can agree on it exactly).
        Objects are immutable; names are a separate mutable index that points into the
        store. Content addressing means there are no namespace wars — names are local,
        hashes are universal.
      </p>
      <p>
        The store is trusted because it is <em>checked</em>, not merely because it is
        content-addressed: an object written straight into the store is re-validated
        on load, because the typechecker and evaluator are not total on malformed
        definitions.
      </p>

      <h2>The trusted gate</h2>
      <p>
        Elaboration turns surface syntax into the canonical AST, resolving names to de
        Bruijn indices and hashes. The typechecker is bidirectional local synthesis —
        type arguments may be omitted and are inferred by one-sided matching, never
        unification of two unknowns — small enough to audit. It also enforces strict
        positivity on datatypes, so a type that would encode non-termination is
        rejected at the gate. Only then does the evaluator run each property under a
        fuel and depth bound.
      </p>

      <h2>The prover</h2>
      <p>
        The prover translates properties to SMT-LIB and discharges them to Z3.
        Recursion is handled by induction — structural and lexicographic for
        shrinking datatypes, and recursion induction for functions that recurse on
        an integer counter (whose totality a Z3-verified ranking function
        establishes). The defining equation of a recursive function is asserted as
        an axiom only when the function is known total, so a non-terminating callee
        is left uninterpreted rather than admitting a false proof. Proven properties become a lemma library — asserted as axioms in
        later proofs, composing bottom-up through the hash graph, with relevance
        filtering so axiom sets are bounded by reachability rather than library size.
        Z3 &quot;unknown&quot; and timeouts are treated as failure, never as proof.
      </p>
      <p>
        Integers are unbounded on both sides of the proof: the solver reasons over
        ℤ, and the kernel&apos;s <code>Int</code> is arbitrary precision at runtime too
        — so a proof carries no &quot;valid modulo overflow&quot; asterisk. Overflow is
        not a defined answer int64 can&apos;t hold; it&apos;s an answer we compute. (A
        compiled program computing 10²⁴ prints the right number, not a wrapped one.)
      </p>

      <h2>Two kernels, one spec</h2>
      <p>
        <code>oath/</code> is the Go reference kernel. <code>oathrs/</code> is an
        independent Rust kernel, built <em>blind</em> from{" "}
        <code>docs/SPEC.md</code> and the fixtures alone — never the Go source. It
        passes all six conformance checks, including byte-identical hashes, matching
        verify transcripts and analyses, and matching proof outcomes. Its independence
        is preserved deliberately: divergences are fixed in the spec and re-derived by a
        blind agent, never patched by copying from the reference. Every ambiguity found
        this way is a recorded spec finding.
      </p>

      <h2>The hosted layer</h2>
      <p>
        <code>oath serve</code> speaks MCP — over stdio for a local, one-store-per-project
        setup, or over HTTP for a team store with principals authenticated by bearer
        token. On the hosted store, journal authorship derives from the token and
        client-supplied author fields are ignored. A repoint policy governs what a{" "}
        <em>name</em> may point at — it can require spec/body authorship separation,
        proven termination, and spec-strength floors before a name moves. Objects always
        store; policy governs only names, so a blocked submission leaves the previous
        version live.
      </p>

      <h2>Publishing: trust by reproduction</h2>
      <p>
        The registry layer needs no trusted server. <code>oath export</code> packs a
        definition&apos;s transitive closure into a single file publishable on any dumb
        host; <code>oath import</code> refuses any byte that doesn&apos;t hash to its
        name, gate-checks in dependency order, and <strong>re-verifies every function
        locally</strong>. Proofs are re-earned, never imported. A registry is just a
        directory of bundles; all trust lives in the importer.
      </p>

      <h2>Compiling to executables</h2>
      <p>
        <code>oath build</code> compiles a definition&apos;s dependency closure to a
        standalone native binary. The provenance gate is the point: an executable is a
        proof-carrying artifact, or it isn&apos;t built — <code>oath build</code>{" "}
        refuses a falsified definition. Compiled programs shed the fuel and depth
        bounds (those are verification semantics); what they keep is provenance.
      </p>
      <p>
        The compiler is where the &quot;prove over the structural model, run over a
        native representation&quot; split happens. A type is proven in whatever form makes
        it provable — a string is an inductive datatype of codepoints, so its laws
        discharge by ordinary induction — but at runtime that same value compiles to a
        native representation: a <code>Str</code> becomes a Go string, not a linked list
        of boxed characters. The two are kept honest by a differential gate — the
        compiled program must produce exactly what the interpreter does — so the native
        representation can never quietly disagree with what was proven. The fast
        execution path and native representations for the other containers are the
        remaining work.
      </p>
    </>
  );
}
