import type { Metadata } from "next";
import localFont from "next/font/local";
import { SITE_ORIGIN } from "@/lib/site";
import "./globals.css";

// SELF-HOSTED, not `next/font/google`. Those three families were fetched from
// fonts.googleapis.com during `next build`, which made a production deploy
// depend on a third party reachable and answering in a parseable shape at build
// time — and on 2026-10-16 it was not: the loader got something its regex did
// not match and the build died on `Cannot read properties of null (reading
// '1')` in a file nothing had touched since August. The same commit deployed
// fine on retry, which is the signature of a dependency that is not ours.
//
// The files are the LATIN subset Google itself serves, committed under
// fonts/OFL.txt. Each family is ONE file carrying a weight AXIS, which is why a
// range is declared rather than a list: requesting 500 and 600 of Cormorant
// returned byte-identical responses, so shipping two copies would have been two
// names for one variable font.
const serif = localFont({
  src: "./fonts/Cormorant.woff2",
  weight: "500 600",
  variable: "--font-serif",
  fallback: ["Georgia", "Times New Roman", "serif"],
  display: "swap",
});

const sans = localFont({
  src: "./fonts/Inter.woff2",
  weight: "400 500",
  variable: "--font-sans",
  fallback: ["system-ui", "-apple-system", "sans-serif"],
  display: "swap",
});

const mono = localFont({
  src: "./fonts/JetBrainsMono.woff2",
  weight: "400 500",
  variable: "--font-mono",
  fallback: ["ui-monospace", "SF Mono", "Menlo", "monospace"],
  display: "swap",
});

export const metadata: Metadata = {
  metadataBase: new URL(SITE_ORIGIN),
  title: {
    default: "Oath Language — Verified code. Immutable truth.",
    template: "%s · Oath Language",
  },
  description:
    "Oath Language is an AI-native verified-codebase kernel. Definitions are content-addressed, carry machine-checked properties in their identity, and live in an immutable object store. Every definition is a sealed promise.",
  applicationName: "Oath Language",
  keywords: [
    "Oath Language",
    "Oath Lang",
    "oath-lang",
    "verified codebase",
    "content-addressed",
    "formal verification",
    "Z3",
    "AI-native language",
  ],
  openGraph: {
    title: "Oath Language — Verified code. Immutable truth.",
    siteName: "Oath Language",
    description:
      "An AI-native verified-codebase kernel: content-addressed identity, machine-checked properties, proofs by Z3. Every definition is a sealed promise.",
    type: "website",
  },
  twitter: {
    card: "summary_large_image",
    title: "Oath Language — Verified code. Immutable truth.",
    description:
      "An AI-native verified-codebase kernel. Every definition is a sealed promise.",
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html
      lang="en"
      className={`${serif.variable} ${sans.variable} ${mono.variable}`}
    >
      <body>{children}</body>
    </html>
  );
}
