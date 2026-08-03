import type { Metadata } from "next";
import { canonicalUrl } from "@/lib/site";

export const metadata: Metadata = {
  title: "Try Oath",
  description:
    "Run Oath Language definitions through the real kernel and Z3 prover in your browser.",
  alternates: { canonical: canonicalUrl("/try") },
};

export default function TryLayout({ children }: { children: React.ReactNode }) {
  return children;
}
