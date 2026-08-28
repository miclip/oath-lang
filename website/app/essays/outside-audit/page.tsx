import type { Metadata } from "next";
import Link from "next/link";
import { canonicalUrl } from "@/lib/site";

export const metadata: Metadata = {
  title: "An Outside Audit",
  description:
    "A skeptic’s read by Codex (GPT-5.5), an independent model that did not build Oath: where the argument holds, where it’s overstated, and what doesn’t generalize.",
  alternates: { canonical: canonicalUrl("/essays/outside-audit") },
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
        the project essays.
      </p>

      <p>
        The strongest evidence is real. The current <code>fixtures/prove/outcomes.json</code>{" "}
        ledger says kernel <code>oath-kernel/0.7</code>, Z3 4.16.0, 232 definitions with
        properties, 635 properties, 382 proven properties, and 141 fully proven
        definitions. It also keeps 87 tested definitions and 4 falsified definitions in
        view. That is a serious artifact, and the site’s browsable corpus data is copied
        from that ledger rather than maintained as a parallel claim.
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
        still converge on the same wrong brief. Hosted MCP has also changed the
        provenance story: <code>registry.oath-lang.org</code> accepts signed JSON-RPC
        requests where the public key is the principal, bearer tokens are read-only
        unless granted write, and state-changing tools are capability-gated. That is a
        real improvement over local self-report. It authenticates who submitted a formal
        claim; it still cannot decide whether the claim captures the intended behavior.
        Publication envelopes now bind the name, artifact, parent, and per-name revision,
        which is the right replay defense for authored transitions. Bearer tokens remain
        server-vouched, the v1 filesystem store does not make the compare-and-set atomic,
        and the spec itself admits tail deletion needs an external anchor.
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
        pure modules. It says less about real systems. Oath now has IEEE-754
        <code>Float</code> with true float laws proven and false ones falsified, real
        capability entry points wired once at the program boundary, and an SMT bridge for
        integer division and modulo away from zero divisors; rational and float division
        are in the proof fragment. Those are material expansions. The remaining boundary
        is narrower but still real: no mutual recursion, partial float narrowings and
        crypto primitives outside proof, capability worlds that serialize one history,
        and no general account yet of time, concurrency, or messy production effects.
      </p>

      <p>
        The public registry matters for the project’s actual audience. This is not only a
        local CLI with a nice ledger: <code>oath serve</code> exposes the substrate as MCP,
        and the live registry gives agents tools for context, discovery, explanation,
        verification, mutation, proof, cross-checking, and submission over HTTPS. The
        capability split is sensible: read tokens can browse, discover, and re-verify;
        writes require a signature or an explicitly write-scoped token; important names
        can be reserved to <code>owner_pubkey</code>; and <code>require_proven</code> can
        defer a name until a worker re-proves it. That strengthens the operational trust
        story. It also adds ordinary operational assumptions: v1 is a single-writer
        hosted store, worker verdict signatures depend on deployment configuration, and
        consumers still need to re-earn proofs rather than treat the registry badge as a
        root of trust.
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
        fixture discipline, hosted write authority, registry operations, and the
        independence of the parties writing claims. That relocation is useful. It gives
        auditors smaller surfaces and better artifacts. Cross-checking and signed MCP
        provenance give the hardest remaining question more machine-visible pressure, but
        not an escape hatch: who writes the oath, and how independent are they really?
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
          <p>
            <strong>2026-07-30 — Registry, MCP, floats, and proof boundaries moved.</strong>{" "}
            The public MCP registry is live, signature auth makes the public key the
            hosted principal, publication envelopes persist the author’s signed transition,
            bearer tokens are read-only unless granted write, and state-changing tools are
            capability-gated. The corpus also grew to 168 definitions, 427 properties, 348
            proven properties, and 123 fully proven definitions; <code>Float</code>, real
            capability entry wiring, and the integer division bridge narrow several
            original objections without removing the final one about intent and
            independence.
          </p>
        </div>
      </details>

      <div className="essay-next">
        <span>The series</span>
        <Link href="/essays">← All essays</Link>
      </div>
    </>
  );
}
