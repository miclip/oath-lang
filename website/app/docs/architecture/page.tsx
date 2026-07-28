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
      <p>
        <code>Rat</code> is ℚ — exact, arbitrary-precision rationals. Decimal
        literals like <code>0.1</code> and fractions like <code>1/2</code> are{" "}
        <code>Rat</code>, so <code>0.1 + 0.2</code> is exactly <code>3/10</code>;
        there is deliberately no <code>Float</code> and no rounding. This is the
        same lens that made strings <em>structural</em>, pointed the other way.
        Z3&apos;s sequence theory is <em>incomplete</em>, so <code>Str</code> is an
        inductive datatype proven by induction; Z3&apos;s linear real arithmetic is{" "}
        <em>complete</em>, so <code>Rat</code> stays a primitive that translates
        straight to the <code>Real</code> sort. The payoff is that the algebraic
        laws IEEE floats violate — associativity, distributivity, exact
        division-inverse <code>(a/b)*b == a</code> — are <em>proven</em>, not merely
        tested. Structure where the solver is weak; primitive where it is strong.
      </p>
      <p>
        <code>Float</code> is the third numeric primitive — IEEE-754 binary64, for
        bit-level interop with the outside world (opt in with an <code>f</code>{" "}
        suffix, <code>0.1f</code>). Z3&apos;s float theory is complete too, so floats
        REACH <code>proven</code>: <code>examples/float.oath</code> proves{" "}
        <code>x * 1.0 == x</code> and <code>x + x == x*2</code> for <em>every</em>{" "}
        float — NaN, ±inf, ±0 included — while <code>0.1f + 0.2f == 0.3f</code> is{" "}
        <code>falsified</code>, because that sum really is{" "}
        <code>0.30000000000000004</code>. The kernel refuses to certify a false
        thing; that is the prover being right, and it is the same property that is{" "}
        <code>proven</code> exact as a <code>Rat</code>. The one subtlety is
        identity: a content-addressed store needs one canonical form per value, so a{" "}
        <code>Float</code> IS its bit pattern (every NaN canonicalized to one), and
        structural <code>==</code> is Leibniz equality — <code>NaN == NaN</code>,{" "}
        <code>+0.0 &ne; -0.0</code> (SMT <code>=</code>), with IEEE&apos;s{" "}
        <code>fp.eq</code> kept as a separate opt-in primitive.
      </p>
      <p>
        The three interconvert explicitly — <code>to-rat</code>,{" "}
        <code>to-float</code>, <code>floor</code>, overloaded by source type. The
        total, exact directions are provable (Z3 <code>to_real</code> /{" "}
        <code>to_fp</code> / <code>to_int</code>): converting an <code>Int</code>{" "}
        into ℚ and flooring back is a proven identity, and the exact rational{" "}
        <code>1/10</code> is proven to round to precisely the <code>0.1f</code>{" "}
        literal. The narrowings that can fail — <code>Float</code> to{" "}
        <code>Rat</code> or <code>Int</code> on a NaN or infinity — fault at
        runtime like division by zero, and stay outside the proof fragment.
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
        setup, or over HTTP for a hosted store. Principals authenticate by an Ed25519
        signature over the request body (the principal <em>is</em> the key — unforgeable,
        no shared secret), with capability-limited bearer tokens as the MCP-client shim
        (read-only unless granted write). A repoint policy governs what a <em>name</em>{" "}
        may point at — it can require spec/body authorship separation, proven termination,
        spec-strength floors, reserve a name to a key (<code>owner_pubkey</code>), or defer
        a name until every property is machine-proven. Objects always store; policy governs
        only names, so a blocked submission leaves the previous version live.
      </p>
      <p>
        A reference instance runs at{" "}
        <a href="https://registry.oath-lang.org">registry.oath-lang.org</a> (deployed from
        CI; the host is not a root of trust — every proof is re-earned by whoever consumes
        a definition).
      </p>

      <h2>Selecting: the decision package</h2>
      <p>
        Finding what satisfies a spec is not the same as knowing whether to use it.{" "}
        <code>oath explain</code> answers the second question: it returns the spec as
        property content hashes, per-property proof status, spec strength and its
        freshness, provenance including whether spec and body had independent authors,
        the exact dependency closure by hash, and — most usefully — the{" "}
        <strong>limitations</strong>, the recorded reasons <em>not</em> to use an
        artifact. It is served over MCP as JSON, because the consumer is an agent
        choosing between candidates.
      </p>
      <p>
        Everything in it is derived from recorded state, so a definition cannot look
        better than its evidence. <code>tested</code> is distinguished from{" "}
        <code>proven</code>. Absent mutation evidence is reported as{" "}
        <code>UNMEASURED</code> rather than as a zero score, because &ldquo;unknown&rdquo;
        and &ldquo;weak&rdquo; are different claims. Waived mutants are listed with their
        justifications, so a caller judges the reasoning rather than trusting the number.
      </p>
      <p>
        The corpus supplies its own argument for why this exists. A spec query for the
        involution law returns two candidates: <code>reverse</code>, proven 2/2 with 3/3
        spec strength, and <code>bad-reverse</code>, which is <em>falsified</em>. Both
        satisfy the queried property. Search alone cannot separate them; the decision
        package does, in a form an agent can act on.
      </p>

      <h2>Campaign identity: evidence, not assertions</h2>
      <p>
        A mutation score of 3/3 answers &ldquo;how many&rdquo; and never &ldquo;out of
        which mutants, under which policy&rdquo;. Evidence without a reproducible identity
        for the computation behind it is an assertion with numbers attached — so a score
        is attached to the digest of a <strong>campaign description</strong>: the
        artifact, the kernel (evaluation semantics decide whether a mutant is caught), the
        mutant generator revision, the execution budget (a survivor at 60 cases may be a
        kill at 600), and the waiver policy and set — because waivers count toward the
        score, so adding one changes the number without changing the code.
      </p>
      <p>
        A consumer compares digests rather than reasoning about version strings and dates.
        Equal means <code>MEASURED</code>: the score was produced under the campaign now
        in force. Different means <code>STALE</code> — authentic evidence, for a
        reproducible campaign other than the current one. Not false, not expired, and
        never presented as current.
      </p>
      <p>
        The registry <em>computes</em> these scores; it never accepts one from a
        publisher. A client-supplied mutation score would be exactly the
        publisher-asserts model this substrate replaces. The encoding is normative
        (SPEC §11), so an auditor can reconstruct a description and check the digest
        instead of trusting that some hex string is the right one.
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

      <h2>Discovery: finding proven code by what it does</h2>
      <p>
        A registry is only useful if you can <em>draw</em> proven code from it instead
        of rebuilding — and that means discovery keyed on meaning, not on names (the one
        non-authoritative layer). It falls out of content-addressing:{" "}
        <strong>properties are content-addressed too</strong>. A property is stored as{" "}
        <code>(binders, body)</code> with the function as <code>self</code> and de Bruijn
        binders, so a pure law like commutativity has one canonical hash wherever it
        appears — &ldquo;which proven definitions satisfy this spec?&rdquo; is a hash
        lookup, not a search.
      </p>
      <p>
        <code>oath find</code> exposes four name-free modes: by example (who shares a
        law with this def?), by a fresh spec you write (<code>self</code> is the sought
        function), matched <em>up to operand types</em> (<code>Int</code> and{" "}
        <code>Rat</code> commutativity match), and — because a property is{" "}
        <em>portable</em> — by <strong>proof-implication</strong>: append your spec to
        each same-signature definition and prove it, so commutativity written{" "}
        <code>(== (self b a) (self a b))</code> still finds <code>+</code> even though its
        AST differs. One invariant runs through all of it, and guards the eventual
        e-graph: the discovery layer draws edges <em>over</em> the hash graph; it never
        touches identity. Semantics is a view, not a redefinition.
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
        of boxed characters. <code>Set</code> and <code>Map</code> — distinct types over
        the sorted-list model they are proven against — likewise compile to native Go
        hash maps, turning membership and lookup into O(1) operations while the proofs
        keep reasoning about the sorted list. The two are kept honest by a differential
        gate — the compiled program must produce exactly what the interpreter does — so
        the native representation can never quietly disagree with what was proven. The
        fast execution path and persistent maps for efficient functional updates are the
        remaining work.
      </p>
      <p>
        There are two entry protocols. A CLI entry is{" "}
        <code>(-&gt; (List Str) Str)</code>, invoked once with argv. A{" "}
        <strong>handler</strong> is <code>(-&gt; Request Response)</code>, invoked per
        request by the host — so <code>oath build</code> emits a program that serves
        HTTP. Ingress is deliberately a protocol rather than a capability: a capability is
        outbound authority a program <em>holds</em> and could misuse, which is why
        confinement checking exists, but being called is not authority. The host owns the
        socket, TLS, routing and lifecycle; the artifact stays a pure function of the
        value it is handed, and inherits every existing gate unchanged.
      </p>
      <p>
        Either protocol may take a leading capability record. Capabilities are wired with
        genuine implementations exactly once, at the program boundary —{" "}
        <code>fetch</code> becomes a real HTTP GET, <code>emit</code> a real append to a
        sink — and everything below that line received authority as an ordinary argument
        and was verified against every simulated world before the real one arrived. The
        compiler refuses an entry that is falsified, unverified, or whose capability the
        confinement checker marks <code>ESCAPES</code>: a program that stores or returns
        its capability never receives the real one.
      </p>
      <p>
        The boundary adapter normalizes as little as it can. A request body crosses as raw{" "}
        <strong>bytes</strong> — <code>(List Int)</code>, one Int per byte, deliberately
        not <code>Str</code>, which is a codepoint list that would corrupt any byte above
        0x7F <em>inside the type</em>, where nothing downstream could recover it. That
        matters because signatures are computed over bytes. Time enters the same way:{" "}
        <code>received-at</code> is a <em>field of the request</em>, supplied once at the
        boundary, not an ambient clock — so a handler stays deterministic and the property
        generator quantifies over it for free.
      </p>
      <p>
        The worked example is <code>webhook</code>: it verifies an HMAC-SHA256 signature
        over the raw body, rejects a stale timestamp, and emits the validated event —
        proven properties for the logic, and exactly one trusted component. The crypto
        primitives sit <em>outside</em> the provable fragment on purpose, because
        modelling SHA-256 in an SMT solver is not useful even where it is possible, and an
        axiomatization invented for the purpose would establish facts about the
        axiomatization rather than about the algorithm. Naming that boundary precisely is
        worth more than pretending it is not there.
      </p>
    </>
  );
}
