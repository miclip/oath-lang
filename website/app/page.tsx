import Link from "next/link";
import { Nav } from "@/components/Nav";
import { Footer } from "@/components/Footer";
import { Pillars } from "@/components/Pillars";
import { CodeBlock } from "@/components/CodeBlock";
import { Logo } from "@/components/Logo";
import { stats, featured } from "@/lib/corpus";

function Badge({ level, children }: { level: string; children: React.ReactNode }) {
  return <span className={`rung-mark v-${level}`}>{children}</span>;
}

export default function Home() {
  return (
    <>
      <Nav />

      {/* ---------------- hero ---------------- */}
      <header className="hero">
        <div className="hero-glow" />
        <div className="wrap">
          <Logo size={104} className="hero-emblem" />
          <h1>OATH</h1>
          <div className="hero-tag">Verified code. Immutable truth.</div>
          <p className="hero-lead">Every definition is a sealed promise.</p>
          <p className="hero-sub">
            Oath is an AI-native verified-codebase kernel. Definitions are
            content-addressed by the hash of their canonical form, carry
            machine-checked properties inside their identity, and live in an
            immutable object store. The syntax is disposable — the substrate is
            the product.
          </p>
          <div className="hero-actions">
            <Link href="/playground" className="btn btn-primary">
              Explore the corpus →
            </Link>
            <Link href="/docs/quickstart" className="btn">
              Quickstart
            </Link>
            <a
              href="https://github.com/miclip/oath-lang"
              target="_blank"
              rel="noreferrer"
              className="btn"
            >
              GitHub ↗
            </a>
          </div>
          <div className="hero-seal">— every definition is a sealed promise —</div>
        </div>
      </header>

      {/* ---------------- stats ---------------- */}
      <section className="wrap" style={{ marginTop: -34, position: "relative", zIndex: 2 }}>
        <div className="stats">
          <div className="stat">
            <div className="stat-num">{stats.definitions}</div>
            <div className="stat-label">definitions</div>
          </div>
          <div className="stat">
            <div className="stat-num">{stats.properties}</div>
            <div className="stat-label">properties</div>
          </div>
          <div className="stat">
            <div className="stat-num">
              <span className="accent">{stats.proven}</span>
            </div>
            <div className="stat-label">proven by Z3</div>
          </div>
          <div className="stat">
            <div className="stat-num">{stats.fullyProven}</div>
            <div className="stat-label">fully proven</div>
          </div>
          <div className="stat">
            <div className="stat-num">2</div>
            <div className="stat-label">kernels agree</div>
          </div>
        </div>
      </section>

      {/* ---------------- substrate ---------------- */}
      <section className="section" id="substrate">
        <div className="wrap">
          <div className="two-up">
            <div>
              <p className="eyebrow">The substrate</p>
              <h2 style={{ fontSize: "clamp(30px,4vw,44px)", marginBottom: 20 }}>
                A codebase that verifies itself before it trusts a name.
              </h2>
              <p className="section-lead">
                Code isn&apos;t files and folders — it&apos;s an immutable graph of
                content-addressed objects. Each definition arrives with its
                specification attached. The kernel typechecks it, runs every
                property, and only then lets a human-readable name point at it.
              </p>
              <ul className="feature-list">
                <li>
                  <span className="mk">◆</span>
                  <span>
                    <strong>Identity is the hash.</strong> Rename every variable —
                    the object is byte-identical. De Bruijn binders make code
                    alpha-canonical by construction.
                  </span>
                </li>
                <li>
                  <span className="mk">◆</span>
                  <span>
                    <strong>The gate is total.</strong> Ill-typed code is refused;
                    every accepted definition has been checked, tested, and
                    verdicted.
                  </span>
                </li>
                <li>
                  <span className="mk">◆</span>
                  <span>
                    <strong>Verdicts compose.</strong> Totality, confinement, and
                    proofs propagate bottom-up through the dependency graph.
                  </span>
                </li>
              </ul>
            </div>
            <div>
              <CodeBlock
                label="examples/list.oath"
                code={`(data List [a]
  (Nil)
  (Cons a (List a)))

(defn length [a] [(xs (List a))] Int
  (match xs
    ((Nil) 0)
    ((Cons h t) (+ 1 (length [a] t))))
  (prop non-negative [(xs (List Int))]
    (<= 0 (length [Int] xs)))
  (prop cons-adds-one [(x Int) (xs (List Int))]
    (== (length [Int] (Cons [Int] x xs))
        (+ 1 (length [Int] xs)))))`}
                verdict={<span className="v-proven rung-mark">proven</span>}
              />
              <p style={{ color: "var(--cream-faint)", fontSize: 13, marginTop: 12 }}>
                The s-expression syntax is an input format. It elaborates to the
                canonical AST and is thrown away; <code style={{ fontSize: 12 }}>oath get</code>{" "}
                projects a fresh one back for human auditors.
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* ---------------- pillars ---------------- */}
      <section className="section" id="pillars">
        <div className="wrap">
          <p className="eyebrow">Six invariants</p>
          <h2 style={{ fontSize: "clamp(28px,4vw,40px)", marginBottom: 14 }}>
            What every object carries.
          </h2>
          <p className="section-lead" style={{ marginBottom: 44 }}>
            Identity, properties, proofs, capabilities, dependencies, metadata —
            the six things that make a definition a promise rather than a
            suggestion.
          </p>
          <Pillars />
        </div>
      </section>

      {/* ---------------- guarantees ---------------- */}
      <section className="section" id="guarantees">
        <div className="wrap">
          <div className="two-up" style={{ alignItems: "start" }}>
            <div>
              <p className="eyebrow">The guarantee ladder</p>
              <h2 style={{ fontSize: "clamp(28px,4vw,40px)", marginBottom: 20 }}>
                Honest about how much it knows.
              </h2>
              <p className="section-lead" style={{ marginBottom: 30 }}>
                A property doesn&apos;t just pass or fail. It sits on a rung, and the
                rung is recorded in the definition&apos;s metadata. Nothing is
                dressed up as more certain than it is.
              </p>
              <div className="ladder">
                <div className="rung">
                  <span className="rung-mark v-asserted">asserted</span>
                  <div className="rung-body">
                    <h4>Stated, not yet checked</h4>
                    <p>The claim exists in the signature.</p>
                  </div>
                </div>
                <div className="rung">
                  <span className="rung-mark v-tested">tested</span>
                  <div className="rung-body">
                    <h4>200 deterministic cases</h4>
                    <p>Inputs seeded from the definition&apos;s own hash — reproducible.</p>
                  </div>
                </div>
                <div className="rung">
                  <span className="rung-mark v-proven">proven</span>
                  <div className="rung-body">
                    <h4>All inputs, via Z3</h4>
                    <p>SMT-LIB translation, structural induction for recursion.</p>
                  </div>
                </div>
                <div className="rung">
                  <span className="rung-mark v-falsified">falsified</span>
                  <div className="rung-body">
                    <h4>Refuted, with a counterexample</h4>
                    <p>A wrong definition is a recorded fact, not a hidden bug.</p>
                  </div>
                </div>
              </div>
            </div>
            <div>
              <p style={{ fontFamily: "var(--font-mono)", fontSize: 12, letterSpacing: "0.16em", textTransform: "uppercase", color: "var(--cream-faint)", marginBottom: 14 }}>
                The exhibits kept on purpose
              </p>
              <CodeBlock
                label="examples/undertested.oath — abs-small"
                code={featured["abs-small"].source}
                verdict={<span className="v-tested rung-mark">tested · refuted by Z3</span>}
              />
              <p style={{ color: "var(--cream-dim)", fontSize: 14, margin: "14px 0 26px" }}>
                Passes all 200 test cases; Z3 refutes it at{" "}
                <code style={{ fontSize: 12 }}>x = -401</code>, an input the generator
                never draws. This is <em>why</em> the rungs differ.
              </p>
              <CodeBlock
                label="examples/nontotal.oath — spin"
                code={featured["spin"].source}
                verdict={<span className="v-falsified rung-mark">falsified</span>}
              />
              <p style={{ color: "var(--cream-dim)", fontSize: 14, marginTop: 14 }}>
                Non-terminating. The type system accepts it; the termination checker
                won&apos;t bless it; the fuel bound turns the loop into an honest
                verdict instead of a hang.
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* ---------------- proof spotlight ---------------- */}
      <section className="section">
        <div className="wrap">
          <div className="two-up">
            <div>
              <CodeBlock
                label="examples/sort.oath — insertion sort"
                code={featured["sort"].source}
                verdict={<span className="v-proven rung-mark">proven · total</span>}
              />
            </div>
            <div>
              <p className="eyebrow">Proof, not vibes</p>
              <h2 style={{ fontSize: "clamp(28px,4vw,40px)", marginBottom: 20 }}>
                Insertion sort, proven correct for every list.
              </h2>
              <p className="section-lead">
                Authored against the <em>specs</em> of{" "}
                <code style={{ fontSize: 13 }}>List</code>,{" "}
                <code style={{ fontSize: 13 }}>length</code>, and{" "}
                <code style={{ fontSize: 13 }}>append</code> — never their bodies.
                The permutation oath is the strong one:{" "}
                <code style={{ fontSize: 13 }}>count x (sort xs) == count x xs</code>.
                Sorted output plus same length can both hold for wrong code; sorted
                plus counts-preserved cannot.
              </p>
              <ul className="feature-list">
                {featured["sort"].notes?.map((n, i) => (
                  <li key={i}>
                    <span className="mk">✓</span>
                    <span>{n}</span>
                  </li>
                ))}
                <li>
                  <span className="mk">✓</span>
                  <span>
                    Proven properties become a <strong>lemma library</strong>,
                    asserted as axioms in later proofs — composing bottom-up through
                    the hash graph.
                  </span>
                </li>
              </ul>
            </div>
          </div>
        </div>
      </section>

      {/* ---------------- capabilities ---------------- */}
      <section className="section">
        <div className="wrap">
          <div className="two-up">
            <div>
              <p className="eyebrow">Effects as capabilities</p>
              <h2 style={{ fontSize: "clamp(28px,4vw,40px)", marginBottom: 20 }}>
                The signature is the authority audit.
              </h2>
              <p className="section-lead">
                No effect system — a capability is just a record of functions passed
                as an ordinary parameter. Purity is visible as the absence of
                capability parameters. Properties quantify over generated simulated
                worlds, and the kernel proves <strong>confinement</strong>: a
                capability that is only exercised — never returned, stored, or
                captured — is verdicted <code style={{ fontSize: 13 }}>confined</code>.
              </p>
              <ul className="feature-list">
                {featured["greet"].notes?.map((n, i) => (
                  <li key={i}>
                    <span className="mk">◆</span>
                    <span>{n}</span>
                  </li>
                ))}
              </ul>
            </div>
            <div>
              <CodeBlock
                label="examples/service.oath — greet"
                code={featured["greet"].source}
                verdict={<span className="v-proven rung-mark">net: confined</span>}
              />
            </div>
          </div>
        </div>
      </section>

      {/* ---------------- two kernels ---------------- */}
      <section className="section" id="kernels">
        <div className="wrap" style={{ textAlign: "center" }}>
          <p className="eyebrow">Trust by reproduction</p>
          <h2 style={{ fontSize: "clamp(28px,4vw,42px)", marginBottom: 18 }}>
            Two kernels. Byte-identical hashes.
          </h2>
          <p className="section-lead" style={{ margin: "0 auto 44px" }}>
            The Go reference kernel and an independent Rust kernel — the second
            built <em>blind</em> from the specification and fixtures alone, never
            the Go source. They agree on every hash, every verdict, and all six
            conformance checks. When identity is a hash, no server has to be
            trusted; a definition is verified by reproducing it.
          </p>
          <div className="pillars" style={{ gridTemplateColumns: "repeat(3,1fr)" }}>
            <div className="pillar">
              <h3 style={{ color: "var(--sage)" }}>oath</h3>
              <div className="pillar-sub">Go reference · zero deps</div>
              <p>
                The normative kernel and CLI. Typechecker, evaluator, prover,
                mutation tester, content-addressed store, MCP server.
              </p>
            </div>
            <div className="pillar">
              <h3 style={{ color: "var(--sage)" }}>oathrs</h3>
              <div className="pillar-sub">Rust · built blind</div>
              <p>
                An N-version implementation from <code style={{ fontSize: 12 }}>SPEC.md</code>{" "}
                + fixtures only. Every ambiguity it surfaced is a recorded spec
                finding.
              </p>
            </div>
            <div className="pillar">
              <h3 style={{ color: "var(--sage)" }}>conformance</h3>
              <div className="pillar-sub">six checks · CI-gated</div>
              <p>
                Byte-identical hashes, verify transcripts, analyses, and every
                proof outcome must match across both kernels, on every change.
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* ---------------- CTA ---------------- */}
      <section className="section" style={{ textAlign: "center", background: "var(--ink-1)" }}>
        <div className="wrap">
          <Logo size={64} className="hero-emblem" />
          <h2 style={{ fontSize: "clamp(28px,4vw,42px)", marginBottom: 16 }}>
            Read the promises for yourself.
          </h2>
          <p className="section-lead" style={{ margin: "0 auto 34px" }}>
            The playground is the real verified corpus — {stats.definitions}{" "}
            definitions with their actual content hashes and Z3 verdicts. Nothing
            mocked.
          </p>
          <div className="hero-actions">
            <Link href="/playground" className="btn btn-primary">
              Open the playground →
            </Link>
            <Link href="/docs" className="btn">
              Read the docs
            </Link>
          </div>
        </div>
      </section>

      <Footer />
    </>
  );
}
