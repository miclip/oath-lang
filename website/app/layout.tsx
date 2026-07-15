import type { Metadata } from "next";
import { Cormorant, Inter, JetBrains_Mono } from "next/font/google";
import "./globals.css";

const serif = Cormorant({
  subsets: ["latin"],
  weight: ["500", "600"],
  variable: "--font-serif",
  fallback: ["Georgia", "Times New Roman", "serif"],
  display: "swap",
});

const sans = Inter({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-sans",
  fallback: ["system-ui", "-apple-system", "sans-serif"],
  display: "swap",
});

const mono = JetBrains_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-mono",
  fallback: ["ui-monospace", "SF Mono", "Menlo", "monospace"],
  display: "swap",
});

export const metadata: Metadata = {
  metadataBase: new URL("https://oath-lang.vercel.app"),
  title: {
    default: "Oath — Verified code. Immutable truth.",
    template: "%s · Oath",
  },
  description:
    "Oath is an AI-native verified-codebase kernel. Definitions are content-addressed, carry machine-checked properties in their identity, and live in an immutable object store. Every definition is a sealed promise.",
  keywords: [
    "Oath",
    "verified codebase",
    "content-addressed",
    "formal verification",
    "Z3",
    "AI-native language",
  ],
  openGraph: {
    title: "Oath — Verified code. Immutable truth.",
    description:
      "An AI-native verified-codebase kernel: content-addressed identity, machine-checked properties, proofs by Z3. Every definition is a sealed promise.",
    type: "website",
  },
  twitter: {
    card: "summary_large_image",
    title: "Oath — Verified code. Immutable truth.",
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
