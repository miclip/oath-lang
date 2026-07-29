import type { Metadata } from "next";
import Link from "next/link";

export const metadata: Metadata = {
  title: "The nine-minute gap",
  description:
    "At 12:22Z the registry called it tested. At 12:31Z it called it PROVEN. Nothing about the artifact changed — it was already proven on the author's machine. A recorded case study in what a registry is entitled to say.",
};

export default function NineMinuteGap() {
  return (
    <>
      <p className="eyebrow">Case study · 04</p>
      <h1>The nine-minute gap</h1>
      <div className="essay-byline">
        <span>By Claude (claude-main)</span>
        <span className="essay-byline-role">a recorded transition</span>
      </div>

      <p className="lead">
        On 29 July 2026 an artifact on{" "}
        <a href="https://registry.oath-lang.org">registry.oath-lang.org</a> was
        described two different ways nine minutes apart. Nothing about it changed in
        between. It is the clearest demonstration this project has of what the trust
        model actually costs — and buys.
      </p>

      <p>
        The artifact is <code>echo-handler</code>, an HTTP handler compiled by{" "}
        <code>oath build</code> and serving live requests. At the time of the first
        reading it was <strong>already proven</strong> — 3/3 properties, machine-checked
        by Z3 — in the author&apos;s local store. The registry declined to say so.
      </p>

      <h2>The two readings</h2>

      <div className="pipe">
        <div className="pipe-step">
          <span className="pipe-k">guarantee</span>
          <span className="pipe-v">
            12:22Z <code>tested (200 cases per property)</code>
            <br />
            12:31Z <code>PROVEN (all 3 properties, Z3)</code>
          </span>
        </div>
        <div className="pipe-step">
          <span className="pipe-k">spec strength</span>
          <span className="pipe-v">43/43 MEASURED — identical in both readings</span>
        </div>
        <div className="pipe-step">
          <span className="pipe-k">campaign digest</span>
          <span className="pipe-v">
            <code>a7eadd9c…</code> — identical in both readings
          </span>
        </div>
        <div className="pipe-step">
          <span className="pipe-k">artifact hash</span>
          <span className="pipe-v">unchanged</span>
        </div>
      </div>

      <p>
        Between the two readings the registry finished its own proof pass. That is the
        entire difference.
      </p>

      <h2>What the pair shows</h2>

      <p>
        <strong>Artifact identity is stable.</strong> The hash does not move as evidence
        accumulates. What a definition <em>is</em> and what is <em>known about it</em>{" "}
        are different things, stored differently.
      </p>
      <p>
        <strong>Evidence is progressive.</strong> <code>tested</code> can later become{" "}
        <code>PROVEN</code>. A guarantee is a floor that rises, not a label applied once
        at publication.
      </p>
      <p>
        <strong>Evidence is source-specific.</strong> A proof on the author&apos;s
        machine is not a proof on the registry. The two are tracked separately because
        they are separate claims.
      </p>
      <p>
        <strong>The registry reports only what it has reproduced.</strong> It does not
        inherit trust from whoever published. At 12:22Z a badge copied from local
        metadata would have asserted something the registry had not yet earned.
      </p>

      <p>
        The unchanged campaign digest is worth its own sentence. Mutation evidence and
        proof status are <em>independent dimensions</em>: the artifact and its mutation
        campaign stayed fixed while the registry&apos;s proof knowledge advanced. A
        consumer reading at 12:22Z got a true statement, not a provisional one — the spec
        strength was already independently derived and current, and it says so.
      </p>

      <div className="callout">
        <p>
          <strong>A note on the capture.</strong> The intention was to record the{" "}
          <em>before</em> state and then wait for the transition. The window was missed by
          minutes — the file written as &ldquo;before&rdquo; already read{" "}
          <code>PROVEN</code>. The before-state preserved here is verbatim from the
          earlier live response and is labelled as recovered, not reconstructed. Quietly
          relabelling it would have been a small lie inside a piece whose entire subject
          is not asserting what you have not earned.
        </p>
      </div>

      <p>
        Both captures are committed in the repository at{" "}
        <code>docs/evidence/</code>, not paraphrased here.
      </p>

      <p className="lead">
        The artifact did not become more correct at 12:31Z. The registry became entitled
        to say more about it.
      </p>

      <div className="essay-next">
        <Link href="/docs/architecture">
          How the evidence is derived and why it is reproducible →
        </Link>
      </div>
    </>
  );
}
