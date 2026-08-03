import type { Metadata } from "next";
import Link from "next/link";
import { canonicalUrl } from "@/lib/site";
import { tutorials } from "@/lib/tutorials";

export const metadata: Metadata = {
  title: "Docs — Tutorials",
  description:
    "Learn Oath by doing: the guarantee ladder, exact numbers, content-addressed identity, authoring and proving a function, compiling a program, and finding proven code.",
  alternates: { canonical: canonicalUrl("/docs/tutorials") },
};

export default function TutorialsIndex() {
  return (
    <>
      <h1>Tutorials</h1>
      <p className="lead">
        Learn the substrate by doing. Each walkthrough runs real commands against
        the committed store and shows the actual output — start at the top for a
        path from &ldquo;what is a verdict?&rdquo; to &ldquo;find proven code by
        what it does.&rdquo;
      </p>
      <div className="tut-list">
        {tutorials.map((t) => (
          <Link key={t.slug} href={`/docs/tutorials/${t.slug}`} className="tut-card">
            <h3>{t.title}</h3>
            <p>{t.blurb}</p>
          </Link>
        ))}
      </div>
    </>
  );
}
