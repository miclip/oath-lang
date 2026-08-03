import type { Metadata } from "next";
import Link from "next/link";
import { refdocs, refdocGroups } from "@/lib/refdocs";
import { canonicalUrl } from "@/lib/site";

export const metadata: Metadata = {
  title: "Docs — Reference",
  description:
    "The full reference set, rendered from the repository markdown: the normative specification, the authority model, licensing, the type and evaluation model, and how to operate a registry.",
  alternates: { canonical: canonicalUrl("/docs/reference") },
};

export default function ReferenceIndex() {
  return (
    <>
      <h1>Reference</h1>
      <p className="lead">
        Every reference document, rendered verbatim from the markdown in the
        repository. The files here are the same ones an implementer reads — the
        specification is normative, and the rest explain why it says what it
        says.
      </p>
      {refdocGroups.map((g) => (
        <section key={g}>
          <h2>{g}</h2>
          <div className="tut-list">
            {refdocs
              .filter((d) => d.group === g)
              .map((d) => (
                <Link key={d.slug} href={`/docs/reference/${d.slug}`} className="tut-card">
                  <h3>{d.title}</h3>
                  <p>{d.blurb}</p>
                </Link>
              ))}
          </div>
        </section>
      ))}
    </>
  );
}
