import type { Metadata } from "next";
import { CodeBlock } from "@/components/CodeBlock";

export const metadata: Metadata = {
  title: "Docs — Licensing",
  description:
    "Licensing as an evidence domain: publishers assert signed terms, the registry derives what a composition permits, and UNSTATED is never permission.",
};

export default function Licensing() {
  return (
    <>
      <h1>Licensing</h1>
      <p className="lead">
        Licensing in Oath is an evidence domain, not a metadata field. It follows the
        same split as every other verdict: the publisher <strong>asserts</strong> terms
        and signs for them, and the registry <strong>derives</strong> what a composition
        permits — reproducibly, under a named and versioned model.
      </p>

      <h2>What this is not</h2>
      <ul>
        <li>
          <strong>Not legal advice.</strong> The registry evaluates a lattice; it does
          not practise law.
        </li>
        <li>
          <strong>Not <code>PROVEN</code>.</strong> Compatibility over a finite lattice
          is decided by evaluation, not proved over unbounded inputs. Reusing that word
          would overload the strongest claim the system makes.
        </li>
        <li>
          <strong>Not a licence scanner.</strong> Nothing infers terms from a LICENSE
          file, a header comment, or a package manifest. Terms are asserted by someone
          who signed for them, or they are <code>UNSTATED</code>.
        </li>
      </ul>

      <h2>Asserting terms</h2>
      <p>
        A licence is asserted inside the publication envelope, so it is covered by the
        author&apos;s signature. <code>--dry-run</code> prints the exact bytes before
        anything is signed — the statement itself, not a summary of it.
      </p>
      <CodeBlock code={`$ oath publish --key mykey.key --license Apache-2.0 mydef.oath

EXACT BYTES TO BE SIGNED (this is the statement, not a summary of it):
  | oath-publish/2
  | op=put
  | name=pow
  | artifact=090cc5373e20…
  | parent=090cc5373e20…
  | parent_rev=1
  | author=65ea5701d92e…
  | license=Apache-2.0`} />
      <p>
        Terms belong to the <strong>publication</strong>, not to the artifact. The same
        code published twice under different terms is two assertions and two
        evaluations — which is what makes dual licensing expressible, and relicensing
        possible without changing a hash. A re-publication of identical content still
        asserts: that is how you relicense, since the code does not change and the terms
        do.
      </p>
      <p>
        The registry does not validate the expression. This layer is the notary for what
        the author signed; checking SPDX syntax would drift publication into policy
        enforcement.
      </p>

      <h2>The verdict has five dimensions</h2>
      <p>
        Four are permissions and one is an obligation, and they combine with{" "}
        <em>opposite polarity</em>. A prohibition anywhere defeats a permission; a
        reciprocal requirement anywhere binds the whole composition.
      </p>
      <CodeBlock code={`commercial use          permission   any NO  → NO
redistribution          permission   any NO  → NO
modification            permission   any NO  → NO
patent grant            permission   any NO  → NO
share-alike obligation  OBLIGATION   any YES → YES`} />
      <p>
        Equivalently: permissions fold as minimum and obligations as maximum over{" "}
        <code>NO &lt; UNSTATED &lt; YES</code>.
      </p>

      <h2>UNSTATED is not permission</h2>
      <p>
        This is where Oath differs most from a licence scanner, and it is the single
        thing to get right. <code>UNSTATED</code> means <em>no evidence</em>, and it is{" "}
        <strong>contagious</strong>: one unknown or unmodelled dependency makes the
        entire composition unknown, however many others granted.
      </p>
      <p>
        Ignorance can establish neither a permission nor the absence of an obligation. A
        consumer may adopt &ldquo;treat UNSTATED as deny&rdquo; or &ldquo;require
        explicit grants&rdquo;; the registry will not choose that for you, and it will
        never quietly upgrade the absence of a prohibition into a grant.
      </p>

      <h2>Every verdict carries an identity</h2>
      <p>
        A verdict is reproducible rather than authoritative, so it is published with a
        digest binding what was evaluated and by what.
      </p>
      <CodeBlock code={`{
  "engine": "oath-license/1",
  "model": "spdx-lattice/1",
  "model_digest": "a3aafb3713e3…",
  "policy": "composition",
  "evaluation_digest": "10a68f83e42f…"
}`} />
      <p>
        The model is bound by <strong>content</strong>, not only by name. Editing one row
        while holding the version string fixed changes every identity — without that, a
        lattice could be altered and every historical verdict would silently mean
        something else.
      </p>
      <p>
        Each consumed input is a triple, and each component answers a question the others
        cannot: the <strong>artifact hash</strong> (which code), the{" "}
        <strong>publication identity</strong> (whose grant), and the{" "}
        <strong>asserted expression</strong> (what terms). Names are discovery paths and
        contribute nothing — renaming a definition does not change its evaluation, while
        repointing a name to different code does.
      </p>

      <h2>The model is published, not built in</h2>
      <p>
        The lattice lives in <code>fixtures/license/model.json</code>, incorporated by
        reference as normative data. It is deliberately not frozen into the
        specification: a legal model is expected to be corrected, and each version is
        already distinguishable, so freezing it would make every correction a
        specification fork.
      </p>
      <p>
        An identifier absent from the model yields <code>UNSTATED</code>, which is safe.
        A compound expression such as <code>MIT OR Apache-2.0</code> is never resolved,
        because choosing a disjunct is a decision with legal consequence and belongs to
        the consumer. Identifiers match by exact octet equality — <code>mit</code>,{" "}
        <code>MIT&nbsp;</code> and <code>(MIT)</code> are not <code>MIT</code>, because a
        registry that helpfully normalised would turn an expression the publisher never
        wrote into a grant.
      </p>

      <h2>What a verdict does not tell you</h2>
      <ul>
        <li>
          Whether the asserted terms are <em>true</em>. The registry notarises a claim; it
          does not audit the claimant.
        </li>
        <li>
          Whether the expression matches what the referenced publication actually
          contains — an open design question, deliberately unresolved rather than quietly
          patched.
        </li>
        <li>
          Anything about jurisdictions, or obligations the five dimensions do not
          represent.
        </li>
      </ul>
    </>
  );
}
