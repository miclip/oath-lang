export const SITE_ORIGIN = "https://www.oath-lang.org";

export function canonicalUrl(path: `/${string}`): string {
  return new URL(path, SITE_ORIGIN).toString();
}
