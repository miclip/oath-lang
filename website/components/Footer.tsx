import Link from "next/link";
import { Logo } from "./Logo";

export function Footer() {
  return (
    <footer className="footer">
      <div className="wrap">
        <div className="footer-grid">
          <div className="footer-col" style={{ maxWidth: 300 }}>
            <div className="wordmark" style={{ marginBottom: 16 }}>
              <Logo size={34} />
              <span className="wordmark-text">
                <span className="wordmark-name" style={{ fontSize: 19 }}>
                  OATH
                </span>
                <span className="wordmark-tag">Verified code. Immutable truth.</span>
              </span>
              <span className="wordmark-lang">Lang</span>
            </div>
            <p style={{ color: "var(--cream-faint)", fontSize: 14 }}>
              Oath Language — an AI-native verified-codebase kernel. The syntax is
              disposable; the substrate is the product.
            </p>
          </div>
          <div className="footer-col">
            <h5>Explore</h5>
            <Link href="/#substrate">The substrate</Link>
            <Link href="/#guarantees">Guarantee ladder</Link>
            <Link href="/#kernels">Two kernels</Link>
            <Link href="/essays">Essays</Link>
            <Link href="/playground">Playground</Link>
          </div>
          <div className="footer-col">
            <h5>Docs</h5>
            <Link href="/docs">Overview</Link>
            <Link href="/docs/quickstart">Quickstart</Link>
            <Link href="/docs/guarantees">Guarantees</Link>
            <Link href="/docs/architecture">Architecture</Link>
          </div>
          <div className="footer-col">
            <h5>Source</h5>
            <a href="https://github.com/miclip/oath-lang" target="_blank" rel="noreferrer">
              GitHub ↗
            </a>
            <a
              href="https://github.com/miclip/oath-lang/blob/main/docs/SPEC.md"
              target="_blank"
              rel="noreferrer"
            >
              Kernel spec ↗
            </a>
            <a
              href="https://github.com/miclip/oath-lang/blob/main/DESIGN.md"
              target="_blank"
              rel="noreferrer"
            >
              Design notes ↗
            </a>
          </div>
        </div>
        <div className="footer-note">
          <span>Built by Michael + Claude · a language designed only for AI authors.</span>
          <span>© 2026 · MIT-licensed kernel</span>
        </div>
      </div>
    </footer>
  );
}
