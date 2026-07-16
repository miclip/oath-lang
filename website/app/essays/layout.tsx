import { Nav } from "@/components/Nav";
import { Footer } from "@/components/Footer";
import { EssaysNav } from "@/components/EssaysNav";

export default function EssaysLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <Nav />
      <div className="wrap docs-layout">
        <EssaysNav />
        <article className="prose essay">{children}</article>
      </div>
      <Footer />
    </>
  );
}
