// Reference-doc manifest. The markdown in docs/*.md is the SINGLE SOURCE;
// `make webdocs` copies it verbatim into website/content/docs/, and
// `make check-web-docs` fails if the copies drift — the same arrangement the
// tutorials use.
//
// WHY A MANIFEST RATHER THAN A DIRECTORY SCAN. Not every file in docs/ is
// public-facing: deploy runbooks and pilot records are operational history and
// would read as documentation if they appeared in a nav. Listing them here makes
// publishing a doc a deliberate act, and the drift gate then keeps what IS listed
// honest. A scan would silently publish the next internal note somebody writes.

export type RefDoc = {
  slug: string;
  file: string; // basename in docs/
  title: string;
  blurb: string;
  group: "Protocol" | "Model" | "Operating a registry";
};

export const refdocs: RefDoc[] = [
  {
    slug: "spec",
    file: "SPEC.md",
    title: "The specification",
    blurb:
      "The normative kernel spec: identity encoding, the gate, verdicts, the journal, signed publication, namespace authority, licence evaluation, and conformance. This is the document a second implementation is written against.",
    group: "Protocol",
  },
  {
    slug: "authority",
    file: "authority.md",
    title: "The authority model",
    blurb:
      "Why authority is a principal, why reservations are explicit while exact-name ownership is inferred, why retention beats denial, and what a third party can check from the journal alone.",
    group: "Protocol",
  },
  {
    slug: "licensing",
    file: "licensing.md",
    title: "Licensing",
    blurb:
      "Licensing as an evidence domain: publishers assert signed terms, the registry derives what a composition permits, and UNSTATED is never permission.",
    group: "Protocol",
  },
  {
    slug: "effects",
    file: "effects.md",
    title: "Effects and capabilities",
    blurb: "The capability model: what a definition may touch, and how confinement is checked.",
    group: "Protocol",
  },
  {
    slug: "discovery",
    file: "discovery.md",
    title: "Discovery",
    blurb:
      "Finding proven code by property content-hash rather than by name — and the invariant that discovery never touches identity.",
    group: "Model",
  },
  {
    slug: "generics",
    file: "generics.md",
    title: "Generics",
    blurb: "Dictionary passing: a type class is a capability record, and combinators are proven over all dictionaries.",
    group: "Model",
  },
  {
    slug: "floats",
    file: "floats.md",
    title: "Floats",
    blurb: "IEEE-754 binary64 identity: bit-identity, canonical NaN, and why == is Leibniz equality.",
    group: "Model",
  },
  {
    slug: "bignum-integers",
    file: "bignum-integers.md",
    title: "Integers",
    blurb: "Int is ℤ — arbitrary precision, no overflow, and what that costs.",
    group: "Model",
  },
  {
    slug: "structural-strings",
    file: "structural-strings.md",
    title: "Strings",
    blurb: "Str as an inductive datatype rather than a primitive, and why the string primitives were deleted.",
    group: "Model",
  },
  {
    slug: "native-containers",
    file: "native-containers.md",
    title: "Sets and maps",
    blurb: "Set and Map compiled to native hash maps, differentially gated against the structural definitions.",
    group: "Model",
  },
  {
    slug: "refinements",
    file: "refinements.md",
    title: "Refinement types",
    blurb:
      "Design only: why refinement identity is syntactic, and why logically-equal refinements are related by discovery rather than by identity.",
    group: "Model",
  },
  {
    slug: "egraph",
    file: "egraph.md",
    title: "Semantic canonicalization",
    blurb: "Body-equivalence via AC-normalization, type-directed — the e-graph behind find --equiv.",
    group: "Model",
  },
  {
    slug: "registry-auth",
    file: "registry-auth.md",
    title: "Registry authentication",
    blurb: "Signatures as principals, capability-limited bearer tokens, and why both exist.",
    group: "Operating a registry",
  },
  {
    slug: "registry-verification",
    file: "registry-verification.md",
    title: "Verification workers",
    blurb: "Proving require_proven names out of band, in dependency order, gated on a proof-state fingerprint.",
    group: "Operating a registry",
  },
  {
    slug: "teamstore",
    file: "teamstore.md",
    title: "The team store",
    blurb: "The hosted layer: authenticated principals, repoint policy, and what a blocked submission records.",
    group: "Operating a registry",
  },
  {
    slug: "store-drivers",
    file: "store-drivers.md",
    title: "Store drivers",
    blurb: "The backend seam: filesystem, in-memory, and the GCS-objects plus Postgres-index cloud driver.",
    group: "Operating a registry",
  },
  {
    slug: "deploy",
    file: "deploy.md",
    title: "Deploying a registry",
    blurb: "The GCP walkthrough: keyless deploy from CI, custom domain, managed TLS.",
    group: "Operating a registry",
  },
];

export const refdocGroups: RefDoc["group"][] = ["Protocol", "Model", "Operating a registry"];

export function refdocBySlug(slug: string): RefDoc | undefined {
  return refdocs.find((d) => d.slug === slug);
}
