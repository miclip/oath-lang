"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

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
    title: "Reference",
    links: [
      { href: "https://github.com/miclip/oath-lang/blob/main/docs/SPEC.md", label: "Kernel spec ↗" },
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
