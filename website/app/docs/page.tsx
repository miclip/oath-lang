import type { Metadata } from "next";
import Link from "next/link";
import { stats } from "@/lib/corpus";

export const metadata: Metadata = {
  title: "Docs — Overview",
  description: "What Oath is and why it exists: an AI-native verified-codebase kernel.",
};

export default function DocsOverview() {
  return (
    <>
      <h1>Overview</h1>
      <p className="lead">
        Oath is the v0 kernel of a single question: what would a programming
        language look like if it were designed <em>only for AI authors</em> — no
        human ergonomics, no files, no style, just verifiability and locality?
      </p>

      <p>
        Definitions are content-addressed — a definition&apos;s identity is the
        SHA-256 of its canonical AST. They carry machine-checkable properties as
        part of their signature, and live in an immutable object database instead
        of source files. Names are metadata. The kernel refuses ill-typed code at
        the gate, runs every property with deterministic inputs before a name is
        trusted, and records an <strong>honest guarantee level</strong> on every
        definition.
      </p>

      <div className="callout">
        <p style={{ margin: 0 }}>
          <strong>The positioning, settled after two external reviews:</strong> the
          syntax is disposable, the substrate is the product. The s-expression
          surface is an input format that elaborates to the canonical AST and is
          thrown away.
        </p>
      </div>

      <h2>What&apos;s real today</h2>
      <ul>
        <li>
          <strong>Two independent kernels.</strong> A Go reference implementation
          (zero dependencies) and a Rust kernel built <em>blind</em> from the
          specification alone. They produce byte-identical hashes and agree on
          every verdict across six conformance checks.
        </li>
        <li>
          <strong>A real guarantee system.</strong> asserted → tested → PROVEN, with
          FALSIFIED as a first-class outcome. {stats.proven} properties across{" "}
          {stats.definitions} definitions are proven for all inputs by Z3;{" "}
          {stats.fullyProven} definitions are fully proven.
        </li>
        <li>
          <strong>Four verdict dimensions.</strong> Termination (a lexicographic
          structural checker), confinement (capability escape tracking), spec
          strength (mutation testing with justified waivers), and provenance (an
          append-only, tamper-evident journal).
        </li>
        <li>
          <strong>Honest exhibits kept on purpose.</strong> A falsified{" "}
          <code>bad-reverse</code>, a non-terminating <code>spin</code>, and an{" "}
          <code>abs-small</code> that tests green but is refuted by proof.
        </li>
      </ul>

      <h2>The shape of a definition</h2>
      <p>
        A definition is an object. Its bytes are the canonical binary encoding of
        its AST; its hash is its name-independent identity. Around it sits mutable
        metadata: the human-readable names that point at it, the guarantee verdicts
        the kernel derived, and the provenance of who put it there. Change the body
        and you have a different object with a different hash — the old promise is
        never silently overwritten.
      </p>

      <p>
        Read on: <Link href="/docs/quickstart">Quickstart</Link> to run the kernel,{" "}
        <Link href="/docs/guarantees">The guarantee ladder</Link> for how much a
        verdict actually means, or <Link href="/docs/architecture">Architecture</Link>{" "}
        for how the pieces fit together.
      </p>
    </>
  );
}
