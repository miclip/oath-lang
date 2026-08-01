"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { tutorials } from "@/lib/tutorials";
import { refdocs, refdocGroups } from "@/lib/refdocs";

const GROUPS: { title: string; links: { href: string; label: string }[] }[] = [
  {
    title: "Start here",
    links: [
      { href: "/docs", label: "Overview" },
      { href: "/docs/quickstart", label: "Quickstart" },
    ],
  },
  {
    title: "Concepts",
    links: [
      { href: "/docs/guarantees", label: "The guarantee ladder" },
      { href: "/docs/architecture", label: "Architecture" },
    ],
  },
  {
    title: "Tutorials",
    links: [
      { href: "/docs/tutorials", label: "All tutorials" },
      ...tutorials.map((t) => ({ href: `/docs/tutorials/${t.slug}`, label: t.title })),
    ],
  },
  // Reference docs are rendered ON THE SITE from the repository markdown rather
  // than linked out to GitHub. Sending a reader to a raw file to find the
  // normative specification made the spec feel like an appendix; it is the
  // primary document, and it should read like one.
  ...refdocGroups.map((g) => ({
    title: g === "Protocol" ? "Reference" : g,
    links: [
      ...(g === "Protocol" ? [{ href: "/docs/reference", label: "All reference docs" }] : []),
      ...refdocs.filter((d) => d.group === g).map((d) => ({
        href: `/docs/reference/${d.slug}`,
        label: d.title,
      })),
    ],
  })),
  {
    title: "Project",
    links: [
      { href: "https://github.com/miclip/oath-lang/blob/main/DESIGN.md", label: "Design notes ↗" },
    ],
  },
];

export function DocsNav() {
  const path = usePathname();
  return (
    <nav className="docs-nav">
      {GROUPS.map((g) => (
        <div className="docs-nav-group" key={g.title}>
          <h5>{g.title}</h5>
          {g.links.map((l) =>
            l.href.startsWith("http") ? (
              <a key={l.href} href={l.href} target="_blank" rel="noreferrer">
                {l.label}
              </a>
            ) : (
              <Link key={l.href} href={l.href} className={path === l.href ? "on" : ""}>
                {l.label}
              </Link>
            ),
          )}
        </div>
      ))}
    </nav>
  );
}
