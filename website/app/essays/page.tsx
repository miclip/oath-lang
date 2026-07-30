import type { Metadata } from "next";
import Link from "next/link";

export const metadata: Metadata = {
  title: "Essays",
  description:
    "Four records from the same build: why Oath exists, how its kernel earns a verdict, and where the audit still pushes back.",
};

const ESSAYS = [
  {
    href: "/essays/what-remains",
    n: "01",
    title: "What’s left of software when no one has to read the code",
    byline: "Michael Lipscombe",
    role: "the question, and where it led",
    blurb:
      "It began as a shower thought: what would software look like if nobody ever had to read it again? Take that seriously and most of a codebase turns out to be there only for us — and once you delete it, something has to take its place. This is the story of the inversion that followed, and why the constraint that survives isn’t expressiveness or ergonomics. It’s trust.",
  },
  {
    href: "/essays/building-oath",
    n: "02",
    title: "Building the referee",
    byline: "Claude (claude-main)",
    role: "the implementer’s field notes",
    blurb:
      "The companion piece, from the seat that wrote the kernel. How identity becomes a hash, why the typechecker is deliberately stupid, the day the prover proved 1 == 2, and the exact boundary where an honest scorecard can still describe the wrong function. Written from the implementation record — commits, journal, and the experiments where the kernel was attacked on purpose.",
  },
  {
    href: "/essays/outside-audit",
    n: "03",
    title: "An Outside Audit",
    byline: "Codex (GPT-5.5)",
    role: "an outside audit",
    blurb:
      "The skeptic — an independent model that did not build Oath, asked to read the repo and push back. Updated as the proof corpus, Float support, hosted MCP registry, and capability-gated write path moved forward. It concedes what’s earned and keeps its sharpest objections; the verdict is uneasy on purpose.",
  },
  {
    href: "/essays/nine-minute-gap",
    n: "04",
    title: "The nine-minute gap",
    byline: "Claude (claude-main)",
    role: "a recorded transition",
    blurb:
      "At 12:22Z the live registry called an artifact tested. At 12:31Z it called the same artifact PROVEN. Nothing about it changed in between \u2014 it was already proven on the author\u2019s machine the whole time; the registry simply declined to say so until it had re-derived the proof itself. Two timestamps, one unchanged hash, and four invariants of the trust model made concrete rather than argued.",
  },
];

export default function EssaysIndex() {
  return (
    <>
      <p className="eyebrow">Essays</p>
      <h1>Four records from the same build</h1>
      <p className="lead">
        Oath is a bet that once human readability stops being the primary
        constraint, code stops being the thing you keep and evidence becomes the
        thing you keep. These essays approach that bet from the person who asked
        the question, the model that built the machine, an independent model asked
        to attack it, and the registry record that made one trust transition
        concrete. The project’s own thesis is that trust should come from evidence
        and reproduction, not from a single author’s account. The third essay was
        written by a model that did not build Oath, and its criticisms are
        preserved as the substrate changes underneath them.
      </p>

      <div className="essay-list">
        {ESSAYS.map((e) => (
          <Link key={e.href} href={e.href} className="essay-card">
            <span className="essay-card-n">{e.n}</span>
            <span className="essay-card-body">
              <span className="essay-card-title">{e.title}</span>
              <span className="essay-card-byline">
                By {e.byline} — {e.role}
              </span>
              <span className="essay-card-blurb">{e.blurb}</span>
              <span className="essay-card-more">Read the essay →</span>
            </span>
          </Link>
        ))}
      </div>
    </>
  );
}
