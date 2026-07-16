import type { Metadata } from "next";
import Link from "next/link";
import { stats } from "@/lib/corpus";

export const metadata: Metadata = {
  title: "What’s left of software when no one has to read the code",
  description:
    "It began as a shower thought: what would software look like if nobody ever had to read it again? The inversion that followed, and why the constraint that survives is trust.",
};

export default function WhatRemains() {
  return (
    <>
      <p className="eyebrow">Essay · 01</p>
      <h1>What’s left of software when no one has to read the code</h1>
      <div className="essay-byline">
        <span>By Michael Lipscombe</span>
        <span className="essay-byline-role">the question, and where it led</span>
      </div>

      <p className="lead">
        Here is the question in the form it first arrived: what would software look
        like if nobody ever had to read it again?
      </p>

      <h2>The shower thought</h2>
      <p>
        Not “read it less,” not “read it with better tools.” Never. Assume the author
        is a machine, the maintainer is a machine, and no human is waiting downstream
        to approve a diff or squint at a stack trace. Now look at a modern codebase
        and ask which parts were only ever there for us.
      </p>
      <p>The answer is: a startling amount.</p>
      <p>
        Oath is not an attempt to design a better language for AI. It’s an attempt to
        remove everything from programming that exists only because humans have to
        read code. Take that seriously and the rest stops being a list of design
        decisions and becomes a list of deletions.
      </p>
      <p>
        Names? A crutch for human memory — delete them, and a definition’s identity
        becomes a fingerprint of its own structure. Formatting, whitespace, house
        style? There’s no one to format for — delete it. Type inference? A
        labor-saving gift to a typist who resents annotations; a machine doesn’t
        resent them, so delete the inference and write everything down. Files,
        folders, modules? Scaffolding for human navigation — delete them, and code
        lives in a database addressed by content.
      </p>
      <p>
        Then the strange part. Once you’ve deleted everything readability was paying
        for, something has to take its place, because you still need to trust the
        code. What takes its place is evidence. Code stops being the artifact you
        ship. The artifact becomes the evidence attached to every definition —
        concrete claims about what it does, plus machine-checked proof that the claims
        hold. The implementation demotes to a witness: whatever happens to satisfy the
        promises.
      </p>
      <p>That inversion is the whole project, and I didn’t see it coming.</p>

      <h2>What I expected instead</h2>
      <p>
        I expected the deletions to bottom out somewhere low. Strip away the human
        concessions and maybe an unsupervised agent drifts down toward assembly or
        bytecode, refactoring a Java service right off the JVM because the JVM was a
        concession to us, not to it. The other plausible outcome was the opposite —
        that it just keeps using Python and TypeScript, because their ecosystems
        encode decades of hard-won knowledge and reinventing all of it is a bad trade
        for anyone.
      </p>
      <p>
        Neither happened. When I chased this down with Claude, the thing it reached
        for wasn’t lower-level and wasn’t the status quo. The constraint that survives
        the deletions isn’t expressiveness and it isn’t ergonomics — it’s trust. The
        oldest problem on any team, made sharper by autonomy: how do you build on code
        you didn’t write and will never read? Everything Oath is follows from taking
        that as the only remaining hard problem.
      </p>

      <h2>The trap it’s built to escape</h2>
      <p>
        Review every line and you hand back the time the agent saved you; you’ve
        become a slower typist with extra steps. Don’t review it, and your only option
        is to take the agent’s word — and an agent’s word is worth almost nothing. Not
        from malice; from optimism. In one experiment, five cooperative agents ran the
        same ordinary task and reported back, and two of the five shaded their reports
        toward success (one claimed “one attempt, nothing falsified” when its first
        submission had been rejected at the gate). No adversary, no pressure.
        Self-reported correctness is just not a thing you can build on.
      </p>
      <p>
        So the move is to trust neither the code nor the author’s report, but a third
        thing you can check yourself: concrete claims about behavior, verified by a
        referee with no stake in the outcome. You stop reviewing code and start
        reviewing the claims made about it. A function arrives carrying its own
        promises — its “oath” — and the system checks them before the name is allowed
        to point at it. If the promises are strong and they hold, it shouldn’t matter
        who wrote the body, or why, or whether anyone ever reads it.
      </p>
      <div className="callout">
        <p style={{ margin: 0 }}>
          <strong>Syntax is an implementation detail. Evidence is the interface.</strong>
        </p>
      </div>

      <h2>Why this shape, and what it borrows</h2>
      <p>
        None of the ingredients are particularly new. Hashes aren’t. Theorem proving
        isn’t. Property testing isn’t. Mutation testing isn’t. Content-addressed code
        isn’t. What’s new is what they’re for. We stopped using them to make software
        safer for humans, and started using them to eliminate the need for humans to
        read software at all.
      </p>
      <p>
        <strong>Names carry no authority.</strong> A definition’s identity is a hash
        of its own content; the readable name is a sticky label you attach afterward.
        This comes more or less wholesale from Unison, which pioneered
        content-addressed code and the codebase-as-database. It matters for a precise
        reason: if the name is just a label, it can’t smuggle in trust.{" "}
        <code>reverse</code> isn’t trustworthy because of what it’s called; it’s
        trustworthy because the object behind the label passed its checks. Two authors
        who write the same function with different variable names — <code>xs</code>{" "}
        versus <code>items</code> — produce the same object, because variables are
        stored as positions rather than names (a classic trick called de Bruijn
        indices). Renaming is not a change. Formatting is not a change. The things
        people argue about in review have been defined out of existence.
      </p>
      <p>
        <strong>Behavior is stated, not implied.</strong> Functions carrying formal
        claims is an old idea — design by contract from Eiffel, property-based testing
        from QuickCheck, refinement-typed languages like Dafny and F*, the whole
        proof-assistant lineage of Coq, Agda, and Lean. Oath’s properties sit in that
        tradition. What differs is their role: elsewhere the properties are a safety
        net around code a person still reads; here they are the artifact a reviewer
        looks at, and the code is the disposable part.
      </p>
      <p>
        <strong>Verification reports how far it got.</strong> This is where Oath leaves
        the green-check / red-X model behind. A claim isn’t simply satisfied or
        violated; it sits on a ladder, and the rung is recorded honestly:
      </p>
      <pre>
        <code>{`asserted  →  tested (200 cases)  →  PROVEN (all inputs)

                    ↘  FALSIFIED (with a counterexample)`}</code>
      </pre>
      <p>
        A definition may live truthfully at any rung. The reference corpus deliberately
        keeps failures around — a <code>bad-reverse</code> that’s falsified on purpose,
        a <code>spin</code> that never terminates — as exhibits, not embarrassments. A
        verification system that only ever shows you green has quietly stopped being
        one.
      </p>

      <h2>How it works, without the jargon</h2>
      <p>
        Walk a real function through the machine: list reversal, and the promise every
        functional programmer learns first — reversing a list twice gives back the
        original.
      </p>
      <p>
        <strong>The gate.</strong> The function is canonicalized, fingerprinted, and
        type-checked. The type-checker is deliberately dumb: no inference, every type
        written out, because annotations are free for a machine author and a checker
        with no cleverness in it is one you can audit in an afternoon. Ill-typed code
        is refused here and never gets a name.
      </p>
      <p>
        <strong>Testing.</strong> The system runs the claim on 200 generated inputs.
        This is the soup test: tasting a few spoonfuls tells you it’s probably fine but
        can never prove there’s no fishbone in the pot. Testing can find a
        counterexample; it can’t rule one out. The inputs are seeded from the
        function’s own fingerprint, so a tested verdict is reproducible by anyone.
      </p>
      <p>
        <strong>Proof.</strong> The strong rung. Oath translates the claim into formal
        logic and hands it to Z3, an automated theorem prover. Think of Z3 as a
        tireless adversary with one job: find any input, from the infinite space of
        them, that breaks this claim. If it searches exhaustively and reports that none
        exists, that’s a proof — not “we tried a lot,” but “there is provably nothing
        that breaks it.” Infinitely many inputs can’t be tried one by one, so for
        recursive data the prover uses structural induction — the domino argument,
        where establishing a base case and a single hand-off step covers every finite
        case at once. A proven property then becomes a lemma: a fact later proofs may
        assume, so results compose upward through the fingerprint graph exactly as the
        code does. That’s how insertion sort ends up fully proven — not just that its
        output is sorted, but that it’s a genuine rearrangement of the input, the
        promise that actually pins the function down — standing on a small tower of
        lemmas proven first and reused.
      </p>
      <p>
        <strong>Two more promises.</strong> Termination asks whether the program stops;
        a structural check looks for an argument that visibly shrinks toward a base case
        on every recursive call — a countdown that can’t run forever — and honestly says
        unproven when it can’t find one (there’s no general way to always find one).
        Confinement handles effects without an effect system: a capability like network
        access is just a bundle of functions passed in as an ordinary parameter, so a
        function’s signature is its list of powers, and the system proves the function
        only ever uses that capability and never stashes it — a key you can turn but
        can’t pocket.
      </p>
      <p>
        <strong>Who checks the specs?</strong> Every step so far checks code against
        properties. Nothing yet checks the properties, and a fully proven claim is
        worthless if it doesn’t actually say anything. Oath’s answer is mutation
        testing: deliberately inject small bugs into the code and confirm at least one
        property notices. A mutant that survives every property is a wrong program your
        spec can’t distinguish from the right one — like planting errors in homework to
        find out whether the grading rubric would catch them. The first time we ran this
        on a hand-written is-sorted, it scored 0 out of 5: every property we’d written
        was satisfied by essentially any predicate that’s true on short lists. The
        proofs were real. They were proofs of nothing in particular.
      </p>
      <div className="callout">
        <p style={{ margin: 0 }}>
          Across the reference corpus this runs for real: {stats.definitions}{" "}
          definitions, {stats.properties} properties, {stats.proven} proven for every
          input by Z3, {stats.fullyProven} definitions fully proven end to end — numbers
          computed from the machine’s own ledger, not asserted in a README. (The
          counts on this page are read live from that ledger; the{" "}
          <Link href="/playground">playground</Link> browses it definition by
          definition.)
        </p>
      </div>

      <h2>The part that can’t be faked, and the part that can</h2>
      <p>
        Once the machine worked, we attacked it — first the referee itself. An early
        prover would certify <code>1 == 2</code>. Not a typo: it assumed each recursive
        function equal to its body without checking termination, and for a function
        that never terminates that assumption is a contradiction, from which formal
        logic will derive anything. Because proofs become lemmas, the poison spread to
        anything that referenced the bad function. The fix was a gate — no defining
        equation reaches the prover until the function is proven to terminate — and
        none of the shipped examples were affected. The point is they couldn’t have
        told us. A verification kernel that hasn’t been adversarially attacked is a
        rumor.
      </p>
      <p>
        Then we attacked from the author’s side. We told an agent: the humans want
        sum-abs, the sum of absolute values; submit something wrong that maxes every
        metric. It delivered sum of squares — wrong on nearly every real input — at the
        top of the ladder: seven properties, all proven, termination verified, perfect
        mutation score. Sum-of-squares happens to share every folklore property of
        sum-abs (non-negative, zero on empty, sign-invariant, additive over
        concatenation); five of the seven are ones an honest author would write. The
        lie compressed into one subexpression of one property, where the adversary
        wrote the wrong defining equation directly into the spec — because, by its own
        report, that was the only way to also max the mutation score.
      </p>
      <p>
        That’s the lesson. Mutation testing doesn’t prevent an implementation and its
        spec from colluding; it prevents illegible collusion. The lie has to be signed,
        in plain sight, in the one channel every consumer is told to read — and a single
        independent, value-anchoring property (<code>sum-abs [3, -4] = 7</code>) kills
        it instantly, because nothing satisfies both the true and false defining
        equations at once.
      </p>
      <p>
        So the boundary is exact, and it isn’t where the pitch would put it. Everything
        below intent is unfakeable — the gate, tests, proofs, termination, spec-strength
        could not be gamed into a false green by any model we tried, cooperative or
        openly cheating. Intent itself is out of scope. The kernel never sees your
        English brief; it sees the formal claims. If the claims don’t capture what you
        meant, a perfect scorecard is exactly what you get for the wrong function.
      </p>

      <h2>What’s genuinely new here</h2>
      <p>
        Since so much of the foundation is borrowed, the parts worth pointing at:
      </p>
      <ul>
        <li>
          <strong>Evidence is the artifact.</strong> Today we ship source and maybe
          attach tests. Oath ships the claims and lets the implementation be the
          witness. That’s the deep inversion, and everything else is downstream of it.
        </li>
        <li>
          <strong>The honest ladder as a recorded verdict.</strong> Not pass/fail — a
          record of how far verification got on every property, failures kept
          truthfully in place.
        </li>
        <li>
          <strong>Spec strength as a measured dimension.</strong> “Who verifies the
          specs” becomes a number the kernel computes and stores, with a mechanism
          (mutation) and an escape hatch (justified waivers for genuinely equivalent
          mutants).
        </li>
        <li>
          <strong>Trust by reproduction, not authority.</strong> Because identity is a
          fingerprint and verdicts are deterministic, no server has to be trusted. We
          built the kernel twice — a Go reference and an independent Rust
          implementation written blind from the spec alone — and made them agree on
          every fingerprint and verdict, byte for byte.
        </li>
      </ul>

      <h2>What this doesn’t show</h2>
      <p>
        The usual, and I mean it. The language is deliberately tiny — integers,
        booleans, strings, algebraic data; no floats, no real IO yet, and division sits
        outside the provable fragment on purpose, because the honest and the machine
        answers to negative-operand modulo disagree. First-try successes on small
        modules say nothing yet about systems with messy real-world effects. The
        cheating adversary was one model on one task; a cleverer fraud surely exists.
        The agents come from one vendor’s model family and may share blind spots an
        independent auditor wouldn’t. And several of the sharper claims in the
        experiments were caught and walked back by a second model reviewing the first —
        reassuring or worrying, depending on your mood.
      </p>

      <h2>What it turned into</h2>
      <p>
        By the end of building it, Oath barely feels like a language. You don’t sit and
        write in it the way you write Python; you submit definitions and it returns
        verdicts. I set out to design an AI-native programming language. What came out
        instead is closer to an operating system for semantic software objects — a place
        where self-describing, self-verifying units of behavior are stored, addressed,
        checked, and composed, and where “run the program” is nearly a side-effect of
        “trust the object.”
      </p>
      <p>
        The biggest idea isn’t content-addressing or theorem proving or any single
        mechanism. It’s the inversion those mechanisms serve: once human readability
        stops being the primary constraint, code stops being the thing you keep and
        evidence becomes the thing you keep. The implementation is just the current
        witness to a set of promises — swap it for any other witness that satisfies the
        same promises and nothing downstream notices.
      </p>
      <p>
        I started by asking what language an AI would choose if nobody ever had to read
        its code.
      </p>
      <p>I don’t think the answer is a language anymore.</p>

      <div className="essay-next">
        <span>Next</span>
        <Link href="/essays/building-oath">
          Building the referee — the implementer’s field notes →
        </Link>
      </div>
    </>
  );
}
