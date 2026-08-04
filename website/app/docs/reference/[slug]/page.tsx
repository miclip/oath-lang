import type { Metadata } from "next";
import { notFound } from "next/navigation";
import Link from "next/link";
import fs from "node:fs";
import path from "node:path";
import { Markdown } from "@/components/Markdown";
import { refdocs, refdocBySlug } from "@/lib/refdocs";
import { canonicalUrl } from "@/lib/site";

export function generateStaticParams() {
  return refdocs.map((d) => ({ slug: d.slug }));
}

function read(slug: string): string | null {
  const d = refdocBySlug(slug);
  if (!d) return null;
  try {
    // Rendered from the COPY, which `make check-web-docs` proves identical to
    // docs/. Reading ../docs directly would work in a dev checkout and fail in a
    // deployed build, where the repository is not present.
    return fs.readFileSync(path.join(process.cwd(), "content", "docs", d.file), "utf8");
  } catch {
    return null;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const { slug } = await params;
  const d = refdocBySlug(slug);
  if (!d) return { title: "Not found" };
  return {
    title: `Docs — ${d.title}`,
    description: d.blurb,
    alternates: { canonical: canonicalUrl(`/docs/reference/${d.slug}`) },
  };
}

export default async function RefDocPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const d = refdocBySlug(slug);
  const md = read(slug);
  if (!d || md == null) notFound();

  return (
    <>
      <p style={{ marginBottom: "1.5rem" }}>
        <Link href="/docs/reference">← All reference docs</Link>
      </p>
      <Markdown>{md}</Markdown>
      <hr style={{ margin: "3rem 0 1.5rem" }} />
      <p style={{ fontSize: "0.9rem", opacity: 0.75 }}>
        Rendered verbatim from <code>docs/{d.file}</code> in the repository. The
        markdown is the single source; this page is a copy checked for drift in
        CI, so what you read here is what an implementer reads.
      </p>
    </>
  );
}
