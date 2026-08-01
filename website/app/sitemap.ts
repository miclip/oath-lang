import type { MetadataRoute } from "next";
import { refdocs } from "@/lib/refdocs";
import { tutorials } from "@/lib/tutorials";

const BASE = "https://oath-lang.org";

// One entry per app-router page. Keep in sync when routes are added/removed.
const ROUTES: Array<{
  path: string;
  changeFrequency: MetadataRoute.Sitemap[number]["changeFrequency"];
  priority: number;
}> = [
  { path: "/", changeFrequency: "weekly", priority: 1.0 },
  { path: "/docs", changeFrequency: "weekly", priority: 0.8 },
  { path: "/docs/quickstart", changeFrequency: "monthly", priority: 0.7 },
  { path: "/docs/architecture", changeFrequency: "monthly", priority: 0.7 },
  { path: "/docs/guarantees", changeFrequency: "monthly", priority: 0.7 },
  { path: "/try", changeFrequency: "monthly", priority: 0.7 },
  { path: "/playground", changeFrequency: "monthly", priority: 0.6 },
  { path: "/essays", changeFrequency: "monthly", priority: 0.6 },
  { path: "/essays/building-oath", changeFrequency: "yearly", priority: 0.5 },
  { path: "/essays/outside-audit", changeFrequency: "yearly", priority: 0.5 },
  { path: "/essays/what-remains", changeFrequency: "yearly", priority: 0.5 },
  // Reference docs and tutorials are DERIVED from their manifests rather than
  // listed by hand. The comment above says "keep in sync when routes are added",
  // which is an instruction a person forgets; these two families now cannot
  // fall out of sync, because adding a doc IS adding its route.
  { path: "/docs/reference", changeFrequency: "weekly", priority: 0.8 },
  ...refdocs.map((d) => ({
    path: `/docs/reference/${d.slug}`,
    changeFrequency: (d.slug === "spec" ? "weekly" : "monthly") as MetadataRoute.Sitemap[number]["changeFrequency"],
    // The specification is the primary document, not an appendix.
    priority: d.slug === "spec" ? 0.9 : 0.6,
  })),
  { path: "/docs/tutorials", changeFrequency: "monthly", priority: 0.7 },
  ...tutorials.map((t) => ({
    path: `/docs/tutorials/${t.slug}`,
    changeFrequency: "monthly" as MetadataRoute.Sitemap[number]["changeFrequency"],
    priority: 0.6,
  })),
];

export default function sitemap(): MetadataRoute.Sitemap {
  const lastModified = new Date();
  return ROUTES.map(({ path, changeFrequency, priority }) => ({
    url: `${BASE}${path}`,
    lastModified,
    changeFrequency,
    priority,
  }));
}
