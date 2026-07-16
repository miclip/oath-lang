"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const GROUPS: { title: string; links: { href: string; label: string }[] }[] = [
  {
    title: "Essays",
    links: [
      { href: "/essays", label: "Overview" },
      { href: "/essays/what-remains", label: "What’s left of software" },
      { href: "/essays/building-oath", label: "Building the referee" },
      { href: "/essays/outside-audit", label: "An outside audit" },
    ],
  },
  {
    title: "Go deeper",
    links: [
      { href: "/docs", label: "Docs" },
      { href: "/docs/guarantees", label: "The guarantee ladder" },
      { href: "/docs/architecture", label: "Architecture" },
    ],
  },
  {
    title: "Reference",
    links: [
      { href: "https://github.com/miclip/oath-lang/blob/main/DESIGN.md", label: "Design notes ↗" },
      { href: "https://github.com/miclip/oath-lang/blob/main/docs/SPEC.md", label: "Kernel spec ↗" },
    ],
  },
];

export function EssaysNav() {
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
