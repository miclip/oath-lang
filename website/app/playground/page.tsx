import type { Metadata } from "next";
import { Nav } from "@/components/Nav";
import { Footer } from "@/components/Footer";
import { CorpusExplorer } from "@/components/CorpusExplorer";
import { PipelineDemo } from "@/components/PipelineDemo";
import { stats } from "@/lib/corpus";

export const metadata: Metadata = {
  title: "Playground",
  description:
    "Explore Oath's real verified corpus: every definition with its actual content hash and Z3 verdict. Nothing mocked.",
};

export default function PlaygroundPage() {
  return (
    <>
      <Nav />
      <main className="wrap pg">
        <div className="pg-head">
          <p className="eyebrow">Live corpus</p>
          <h1 style={{ fontSize: "clamp(34px,5vw,52px)", marginBottom: 14 }}>
            The playground is the proof ledger.
          </h1>
          <p className="section-lead">
            This isn&apos;t a sandbox with fake data. Every row below is a real
            definition from the committed Oath store — its content hash is the
            actual SHA-256 identity, and each verdict is the actual outcome the
            kernel recorded when it ran <code style={{ fontSize: 13 }}>oath prove</code>{" "}
            over the corpus. {stats.definitions} definitions, {stats.properties}{" "}
            properties, {stats.proven} proven for all inputs by Z3.
          </p>
          <PipelineDemo />
        </div>

        <CorpusExplorer />

        <p
          style={{
            color: "var(--cream-faint)",
            fontFamily: "var(--font-mono)",
            fontSize: 12.5,
            marginTop: 32,
            lineHeight: 1.7,
          }}
        >
          Standing caveat, kept honest: Z3 reasons over unbounded integers; the
          evaluator uses int64. Division stays outside the proof fragment on
          purpose — the kernel truncates while SMT-LIB is Euclidean, so a
          &quot;proof&quot; would certify the wrong theorem. A proof is valid modulo
          overflow, and the corpus says so.
        </p>
      </main>
      <Footer />
    </>
  );
}
