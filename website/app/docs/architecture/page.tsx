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
        Bruijn indices and hashes. The typechecker is pure structural synthesis — no
        inference, no unification — small enough to audit. It also enforces strict
        positivity on datatypes, so a type that would encode non-termination is
        rejected at the gate. Only then does the evaluator run each property under a
        fuel and depth bound.
      </p>

      <h2>The prover</h2>
      <p>
        The prover translates properties to SMT-LIB and discharges them to Z3.
        Recursion is handled by structural induction; the defining equation of a
        recursive function is asserted as an axiom only when the function is known
        total, so a non-terminating callee is left uninterpreted rather than admitting
        a false proof. Proven properties become a lemma library — asserted as axioms in
        later proofs, composing bottom-up through the hash graph, with relevance
        filtering so axiom sets are bounded by reachability rather than library size.
        Z3 &quot;unknown&quot; and timeouts are treated as failure, never as proof.
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
    </>
  );
}
