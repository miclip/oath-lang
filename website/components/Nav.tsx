"use client";

import Link from "next/link";
import { useState } from "react";
import { Logo } from "./Logo";

const LINKS: { href: string; label: string; cta?: boolean; external?: boolean }[] = [
  { href: "/#substrate", label: "Substrate" },
  { href: "/#guarantees", label: "Guarantees" },
  { href: "/essays", label: "Essays" },
  { href: "/playground", label: "Playground" },
  { href: "/try", label: "Try it" },
  { href: "/docs", label: "Docs" },
  { href: "https://github.com/miclip/oath-lang", label: "GitHub ↗", cta: true, external: true },
];

function NavItem({ href, label, cta, external, onClick }: { href: string; label: string; cta?: boolean; external?: boolean; onClick?: () => void }) {
  if (external) {
    return (
      <a href={href} target="_blank" rel="noreferrer" className={cta ? "nav-cta" : undefined} onClick={onClick}>
        {label}
      </a>
    );
  }
  return (
    <Link href={href} className={cta ? "nav-cta" : undefined} onClick={onClick}>
      {label}
    </Link>
  );
}

export function Nav() {
  const [open, setOpen] = useState(false);
  return (
    <nav className="nav">
      <div className="wrap nav-inner">
        <Link href="/" className="wordmark" aria-label="Oath Language home" onClick={() => setOpen(false)}>
          <Logo size={30} />
          <span className="wordmark-text">
            <span className="wordmark-name" style={{ fontSize: 18 }}>
              OATH
            </span>
          </span>
          <span className="wordmark-lang">Lang</span>
        </Link>
        <div className="nav-links">
          {LINKS.map((l) => (
            <NavItem key={l.href} {...l} />
          ))}
        </div>
        <button
          type="button"
          className="nav-toggle"
          aria-label={open ? "Close menu" : "Open menu"}
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          {open ? "✕" : "☰"}
        </button>
      </div>
      {open && (
        <div className="nav-panel">
          {LINKS.map((l) => (
            <NavItem key={l.href} {...l} onClick={() => setOpen(false)} />
          ))}
        </div>
      )}
    </nav>
  );
}
