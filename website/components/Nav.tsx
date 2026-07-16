import Link from "next/link";
import { Logo } from "./Logo";

export function Nav() {
  return (
    <nav className="nav">
      <div className="wrap nav-inner">
        <Link href="/" className="wordmark" aria-label="Oath Language home">
          <Logo size={30} />
          <span className="wordmark-text">
            <span className="wordmark-name" style={{ fontSize: 18 }}>
              OATH
            </span>
          </span>
          <span className="wordmark-lang">Lang</span>
        </Link>
        <div className="nav-links">
          <Link href="/#substrate">Substrate</Link>
          <Link href="/#guarantees">Guarantees</Link>
          <Link href="/essays">Essays</Link>
          <Link href="/playground">Playground</Link>
          <Link href="/docs">Docs</Link>
          <a
            href="https://github.com/miclip/oath-lang"
            target="_blank"
            rel="noreferrer"
            className="nav-cta"
          >
            GitHub ↗
          </a>
        </div>
        <div className="nav-mobile">
          <Link href="/essays">Essays</Link>
          <Link href="/playground">Playground</Link>
          <Link href="/docs">Docs</Link>
        </div>
      </div>
    </nav>
  );
}
