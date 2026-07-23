import type { MetadataRoute } from "next";

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
