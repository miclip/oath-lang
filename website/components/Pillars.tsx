import { JSX } from "react";

const S = {
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.4,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
};

function IconIdentity() {
  return (
    <svg width="30" height="30" viewBox="0 0 24 24" {...S}>
      <path d="M12 2.5 20 7v10l-8 4.5L4 17V7z" />
    </svg>
  );
}
function IconProperties() {
  return (
    <svg width="30" height="30" viewBox="0 0 24 24" {...S}>
      <ellipse cx="12" cy="5" rx="8" ry="2.6" />
      <path d="M4 5v6c0 1.4 3.6 2.6 8 2.6s8-1.2 8-2.6V5" />
      <path d="M4 11v6c0 1.4 3.6 2.6 8 2.6s8-1.2 8-2.6v-6" />
    </svg>
  );
}
function IconProofs() {
  return (
    <svg width="30" height="30" viewBox="0 0 24 24" {...S}>
      <circle cx="12" cy="8" r="5.5" />
      <path d="M9.6 8l1.7 1.7L14.6 6.4" />
      <path d="M12 13.5V21M8 21h8" />
    </svg>
  );
}
function IconCapabilities() {
  return (
    <svg width="30" height="30" viewBox="0 0 24 24" {...S}>
      <path d="M12 2.5 20 5v6c0 5-3.4 8.4-8 10.5C7.4 19.4 4 16 4 11V5z" />
    </svg>
  );
}
function IconDependencies() {
  return (
    <svg width="30" height="30" viewBox="0 0 24 24" {...S}>
      <circle cx="6" cy="18" r="2.2" />
      <circle cx="18" cy="6" r="2.2" />
      <circle cx="6" cy="7" r="2.2" />
      <path d="M8 16.6 16 7.4M8 7.6l7.6 8.6M8 7h.1" />
    </svg>
  );
}
function IconMetadata() {
  return (
    <svg width="30" height="30" viewBox="0 0 24 24" {...S}>
      <path d="M4 6h16M4 12h16M4 18h10" />
    </svg>
  );
}

interface Pillar {
  icon: () => JSX.Element;
  name: string;
  sub: string;
  body: string;
}

const PILLARS: Pillar[] = [
  {
    icon: IconIdentity,
    name: "Identity",
    sub: "content-addressed",
    body: "A definition's name is the SHA-256 of its canonical AST. Rename every variable and the hash is unchanged — binders are de Bruijn indices, so formatting and naming diffs cannot exist.",
  },
  {
    icon: IconProperties,
    name: "Properties",
    sub: "machine-checkable",
    body: "Every definition carries its specification as part of its signature. The kernel refuses ill-typed code at the gate and runs every property before a name is ever trusted.",
  },
  {
    icon: IconProofs,
    name: "Proofs",
    sub: "tested or proven",
    body: "Properties climb an honest ladder: asserted → tested with deterministic cases → PROVEN for all inputs by Z3, including recursive functions by induction — structural, lexicographic, and recursion induction for integer counters.",
  },
  {
    icon: IconCapabilities,
    name: "Capabilities",
    sub: "authority-bounded",
    body: "Effects are capabilities passed as ordinary parameters — the signature is the authority audit. The kernel proves confinement: a capability that never escapes is verdicted so.",
  },
  {
    icon: IconDependencies,
    name: "Dependencies",
    sub: "explicit graph",
    body: "Definitions reference each other by hash, forming an acyclic graph. Verdicts — totality, confinement, proofs — compose bottom-up through it like a lemma library.",
  },
  {
    icon: IconMetadata,
    name: "Metadata",
    sub: "names are metadata",
    body: "Names, docs, and guarantees live beside the object, never inside its identity. The store is an immutable object database; names are a mutable index that points into it.",
  },
];

export function Pillars() {
  return (
    <div className="pillars">
      {PILLARS.map((p) => (
        <div className="pillar" key={p.name}>
          <div className="pillar-icon">
            <p.icon />
          </div>
          <h3>{p.name}</h3>
          <div className="pillar-sub">{p.sub}</div>
          <p>{p.body}</p>
        </div>
      ))}
    </div>
  );
}
