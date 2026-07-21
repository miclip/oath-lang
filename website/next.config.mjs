/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  async headers() {
    // The /try playground proves novel definitions by running Z3 in a Web Worker,
    // which needs SharedArrayBuffer — and that needs cross-origin isolation.
    const coop = { key: "Cross-Origin-Opener-Policy", value: "same-origin" };
    const coep = { key: "Cross-Origin-Embedder-Policy", value: "require-corp" };
    const corp = { key: "Cross-Origin-Resource-Policy", value: "same-origin" };
    return [
      // The isolated document.
      { source: "/try", headers: [coop, coep] },
      // Its worker + wasm subresources must be embeddable in an isolated context.
      { source: "/_next/:path*", headers: [coep, corp] },
      { source: "/pgrt/:path*", headers: [coep, corp] },
    ];
  },
};
export default nextConfig;
