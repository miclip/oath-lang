import type { Metadata } from "next";
import { notFound } from "next/navigation";
import Link from "next/link";
import fs from "node:fs";
import path from "node:path";
import ReactMarkdown from "react-markdown";
import { canonicalUrl } from "@/lib/site";
import { tutorials, tutorialBySlug } from "@/lib/tutorials";

export function generateStaticParams() {
  return tutorials.map((t) => ({ slug: t.slug }));
}

function read(slug: string): string | null {
  const p = path.join(process.cwd(), "content", "tutorials", `${slug}.md`);
  try {
    return fs.readFileSync(p, "utf8");
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
  const t = tutorialBySlug(slug);
  if (!t) return { title: "Tutorial not found" };
  return {
    title: `Tutorial — ${t.title}`,
    description: t.blurb,
    alternates: { canonical: canonicalUrl(`/docs/tutorials/${t.slug}`) },
  };
}

export default async function TutorialPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const t = tutorialBySlug(slug);
  const md = read(slug);
  if (!t || md == null) notFound();

  // The markdown files lead with an `# H1`; ReactMarkdown renders it, and the
  // shared `.prose` styles from the docs layout do the rest.
  return (
    <>
      <p style={{ marginBottom: "1.5rem" }}>
        <Link href="/docs/tutorials">← All tutorials</Link>
      </p>
      <ReactMarkdown>{md}</ReactMarkdown>
    </>
  );
}
