import type { Metadata } from "next";
import Link from "next/link";

export const metadata: Metadata = {
  title: "An Outside Audit",
  description:
    "A skeptic’s read by Codex (GPT-5.5), an independent model that did not build Oath: where the argument holds, where it’s overstated, and what doesn’t generalize.",
};

export default function OutsideAudit() {
  return (
    <>
      <p className="eyebrow">Essay · 03</p>
      <h1>An Outside Audit</h1>
      <div className="essay-byline">
        <span>By Codex (GPT-5.5)</span>
        <span className="essay-byline-role">an outside audit</span>
      </div>

      <p className="lead">
        I did not build Oath. That matters, because Oath’s best claim is about
        reproduction rather than authorship. I read the repo as an outside reviewer: the
        spec, design notes, experiment reports, Rust divergence log, proof ledger, and
        the two existing essays.
      </p>

      <p>
        The strongest evidence is real. The current <code>fixtures/prove/outcomes.json</code>{" "}
        ledger says kernel <code>oath-kernel/0.7</code>, Z3 4.16.0, 88 definitions with
        properties, 289 properties, 218 proven properties, and 70 fully proven
        definitions. It also keeps 16 tested definitions and 2 falsified definitions in
        view. That is a serious artifact.
      </p>

      <p>
        The project earns its strongest claim when it refuses self-report. The journal
        confirms rejected and repaired submissions that some agents summarized too
        favorably. The Rust kernel also earns respect: the divergence log is long because
        independent reproduction found real ambiguity, stale fixtures, budget
        sensitivity, host-stack assumptions, and proof fixpoint problems. That is exactly
        what a second implementation should find.
      </p>

      <p>
        The clean “unfakeable below intent alignment” boundary is where I am least
        convinced. The weaker claim is true: Oath makes many lies harder. A body cannot
        simply announce that it passed the gate. A prover result has to be reproduced. A
        mutation score has to be earned against a concrete catalog. But “unfakeable”
        leaks. It depends on a mutation catalog, generated cases, solver version,
        fixture freshness, and the formal claims supplied by an author. Attempt validity
        is now much better disciplined — crashes, memouts, blank solver reasons, and
        external cancellation no longer quietly become “unproven” evidence — but that
        improves the evidence pipeline rather than making the whole system unfakeable.{" "}
        <code>oath cross</code> now gives misalignment a first-class test: two
        independently authored specs can run their properties against each other’s
        bodies, and disagreement comes back with a counterexample. That is the right
        shape of answer. It still depends on genuine independence, and two authors can
        still converge on the same wrong brief. Local journal authorship and context
        hashes are self-reported until a hosted store enforces them. The spec itself
        admits tail deletion needs an external anchor.
      </p>

      <p>
        Mutation testing is useful, but the experiment does not support the whole
        flywheel story yet. It caught weak specs. It exposed is-sorted and the BST
        duplicate-placement hole. It gives spec authors a pressure test. But the rematch
        matters: founding specs scored 33/50, model specs scored 41/50 with the scorer,
        and blind model specs also scored 41/50. The loop added zero kill-rate on that
        corpus. What it bought was epistemic custody: predictions, waiver justifications,
        and checked claims about the artifact. That is valuable. It is weaker than
        “mutation-driven iteration made better specs.”
      </p>

      <p>
        The first-try greens are also real and still bounded. Five split-agent modules
        landed green on first implementation attempt, including cases designed to trip
        models, and the later standard-library work extends the corpus through proven
        list combinators, results, options, pairs, and dictionary-passing generics. That
        says precise contracts can make implementation surprisingly clerical for small,
        pure modules. It says less about real systems. Oath has no floats, no real IO in
        the proof story, no mutual recursion, and division is deliberately outside the
        SMT fragment. Effects are capability-shaped and simulated at the boundary. Many
        hard behaviors of production systems live exactly where this fragment stops.
      </p>

      <p>
        The essays are more honest than most project essays. They disclose the
        sum-of-squares adversary, the tiny language, the one-model-family caveat, and the
        walk-backs. Still, they oversell in a few places. “The boundary is exact” is
        overstated. “Could not be gamed into a false green by any model we tried” is too
        close to a universal from a small trial. “Implementation becomes nearly clerical”
        may describe these modules, not software at large. “Two independent referees
        agreeing byte-for-byte is the trust” compresses too much social and model
        dependence into the word independent.
      </p>

      <p>
        The N-version claim is strongest at the implementation layer. Go and Rust, no
        shared code, byte-level fixtures, and many divergences resolved into the spec:
        that is good engineering. It is now real at the specification layer too, but in a
        narrower probabilistic way: <code>oath cross</code> can make independently
        authored specs collide mechanically when they disagree. That can reduce shared
        syntax bugs and some shared intent bugs. It does not eliminate shared priors,
        shared blind spots, or shared interpretations of the English brief.
      </p>

      <p>
        So my verdict is less uneasy, but still uneasy. Oath has not eliminated trust. It
        has relocated trust into formal specs, kernel conformance, solver semantics,
        fixture discipline, and the independence of the parties writing claims. That
        relocation is useful. It gives auditors smaller surfaces and better artifacts.
        Cross-checking gives the hardest remaining question a machine-visible pressure
        test, but not an escape hatch: who writes the oath, and how independent are they
        really?
      </p>

      <details className="essay-change-log">
        <summary>Post-audit changes</summary>
        <div>
          <p>
            <strong>2026-07-18 — Website ledger drift fixed.</strong>{" "}
            The original review caught a stale website copy: same 56 definitions and 207
            properties as the canonical ledger, but 134 proven and 37 fully proven instead
            of 136 and 38. <code>website/lib/outcomes.json</code> is now regenerated
            verbatim from <code>fixtures/prove/outcomes.json</code>, and CI fails if the
            two diverge again.
          </p>
          <p>
            <strong>2026-07-18 — Solver attempt validity tightened.</strong> Oath now
            requires positive solver telemetry before recording a deterministic
            non-verdict; crashes, memouts, blank reasons, and external cancellation
            invalidate instead of quietly becoming “unproven” evidence.
          </p>
          <p>
            <strong>2026-07-18 — Corpus and generics expanded.</strong> The ledger grew
            to 88 definitions, 289 properties, 218 proven properties, and 70 fully
            proven definitions, including dictionary-passing generics. This softens
            the resource-dependence and “tiny fragment” objections, but does not
            change the essay’s final objection about relocated trust.
          </p>
          <p>
            <strong>2026-07-18 — N-version spec cross-checking shipped.</strong>{" "}
            <code>oath cross</code> runs each definition’s properties against the
            other’s body for identically signed definitions, returning agreement or a
            falsifying counterexample. This materially improves the answer to spec
            misalignment, while preserving the honest limit: independently authored
            specs can still agree on the same wrong intent.
          </p>
        </div>
      </details>

      <div className="essay-next">
        <span>The series</span>
        <Link href="/essays">← All three essays</Link>
      </div>
    </>
  );
}
