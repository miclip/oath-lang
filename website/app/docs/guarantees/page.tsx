import type { Metadata } from "next";
import { CodeBlock } from "@/components/CodeBlock";
import { featured } from "@/lib/corpus";

export const metadata: Metadata = {
  title: "Docs — The guarantee ladder",
  description:
    "How much an Oath Language verdict actually means: asserted, tested, proven, falsified — and the four verdict dimensions.",
};

export default function Guarantees() {
  return (
    <>
      <h1>The guarantee ladder</h1>
      <p className="lead">
        A property does not merely pass or fail. It sits on a rung, and the rung is
        recorded in the definition&apos;s metadata. Oath&apos;s central discipline is
        never dressing up a claim as more certain than it is.
      </p>

      <h2>The rungs</h2>
      <h3>asserted</h3>
      <p>
        The property exists in the signature but has not been checked. It is a claim,
        nothing more.
      </p>
      <h3>tested</h3>
      <p>
        The property held for 200 generated inputs. Generation is deterministic —
        seeded from the definition&apos;s own hash — so a tested verdict is
        reproducible byte-for-byte. Testing can only ever find counterexamples, never
        rule them out.
      </p>
      <h3>proven</h3>
      <p>
        The property was translated to SMT-LIB and Z3 held it for <em>all</em> inputs.
        Datatypes become SMT algebraic datatypes, recursive functions become
        quantified defining equations, matches become tester/selector chains, records
        become single-constructor datatypes, and function values are array-encoded —
        so higher-order properties quantify over all functions and capability
        properties over all worlds. Recursion is handled by induction —
        structural and lexicographic for shrinking datatypes, and recursion
        induction for functions that recurse on an integer counter (proving
        measure laws like <code>length (replicate n x) == n</code>).
      </p>
      <h3>falsified</h3>
      <p>
        A counterexample was found. This is a first-class, recorded outcome — a wrong
        definition is a fact in the store, not a hidden bug. <code>oath build</code>{" "}
        refuses to compile an executable from a falsified oath.
      </p>

      <div className="callout">
        <p>
          <strong>Why the rungs differ, in one exhibit.</strong> <code>abs-small</code>{" "}
          passes all 200 test cases and is then refuted by Z3 at an input the
          generator never draws.
        </p>
      </div>
      <CodeBlock
        label="examples/undertested.oath — abs-small"
        code={featured["abs-small"].source}
        verdict={<span className="rung-mark v-tested">tested · refuted at x = -401</span>}
      />

      <h2>The standing caveat</h2>
      <p>
        Z3 reasons over unbounded integers; the evaluator uses int64. A proof is valid{" "}
        <em>modulo overflow</em>, and the corpus says so. Division and modulo stay
        outside the proof fragment on purpose: the kernel truncates while SMT-LIB is
        Euclidean, so a &quot;proof&quot; over division would certify the wrong
        theorem.
      </p>

      <h2>Four dimensions, not one</h2>
      <p>Alongside property proofs, every definition carries three more verdicts:</p>
      <ul>
        <li>
          <strong>Termination.</strong> A structural checker proves totality where
          recursion visibly descends — including lexicographic descent, so{" "}
          <code>merge</code> (which alternates which argument shrinks) is still{" "}
          <code>total</code> — and a Z3-verified ranking function proves it for
          integer-counter recursion, where the counter is not a shrinking
          datatype (<code>replicate</code>, <code>range</code>, the count inside{" "}
          <code>rle-expand</code>&apos;s <code>Run</code>). Genuinely
          non-terminating or unanalyzable recursion is honestly{" "}
          <code>termination unproven</code> and fuel-bounded.
        </li>
        <li>
          <strong>Confinement.</strong> A capability parameter that is only exercised —
          never returned, stored, or captured — is verdicted <code>confined</code>.
          Capability-hoarders are marked <code>ESCAPES</code>.
        </li>
        <li>
          <strong>Spec strength.</strong> Mutation testing asks the question &quot;who
          verifies the specs?&quot; — does a property notice when the body changes?
          Survivors are printed with their bodies; judged-equivalent mutants can be
          waived with justification, and waivers report separately from kills.
        </li>
        <li>
          <strong>Provenance.</strong> Every put attempt — accepted, falsified, or
          rejected — is retained in an append-only journal with principal attribution,
          timestamp, and verifier version.
        </li>
      </ul>

      <div className="callout">
        <p style={{ margin: 0 }}>
          <strong>Verdicts are structure × seed facts.</strong> Mutation scores and
          waivers derive from the definition&apos;s hash; they are never carried across
          an identity change. A fixture is only evidence once you know it was
          regenerated under the current identity.
        </p>
      </div>
    </>
  );
}
