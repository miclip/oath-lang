// Tutorial manifest. The markdown lives in docs/tutorial/*.md (the single
// source); `make tutorials` copies it verbatim into website/content/tutorials/,
// where the pages render it. Order here is the intended learning path.
export type Tutorial = { slug: string; title: string; blurb: string };

export const tutorials: Tutorial[] = [
  {
    slug: "guarantee-ladder",
    title: "The guarantee ladder",
    blurb:
      "asserted → tested → proven → falsified, and mutation testing — how much a verdict actually means, on real definitions.",
  },
  {
    slug: "numbers",
    title: "Numbers you can trust",
    blurb:
      "Int (no overflow), exact Rat, IEEE Float — and the same 0.1 + 0.2 == 0.3 proven for one and falsified for the other.",
  },
  {
    slug: "names",
    title: "Names aren't identity",
    blurb:
      "put, hashes, repointing — why a name collision can never silently change what your code computes.",
  },
  {
    slug: "stdlib",
    title: "Writing and proving a function",
    blurb:
      "Author str-append from scratch, state its laws, watch Z3 prove them, and check the spec is actually tight.",
  },
  {
    slug: "circle",
    title: "A compiled circle calculator",
    blurb:
      "Read a radius, print the area over exact rational π, compiled to a native binary — the whole stack in one program.",
  },
  {
    slug: "discovery",
    title: "Finding proven code by what it does",
    blurb:
      "The four find modes end to end — by example, by a fresh spec, by proof, and by the e-graph — with no name trusted.",
  },
  {
    slug: "reproduce",
    title: "Reproduce a program on a fresh machine",
    blurb:
      "resolve → clone → build: pin a program's dependency closure to a lockfile, then reconstruct it — verified, hash-for-hash — on a fresh machine that fetches the closure from a store or a URL, no dependencies pre-installed.",
  },
];

export function tutorialBySlug(slug: string): Tutorial | undefined {
  return tutorials.find((t) => t.slug === slug);
}
