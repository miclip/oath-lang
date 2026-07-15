import { Nav } from "@/components/Nav";
import { Footer } from "@/components/Footer";
import { DocsNav } from "@/components/DocsNav";

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <Nav />
      <div className="wrap docs-layout">
        <DocsNav />
        <article className="prose">{children}</article>
      </div>
      <Footer />
    </>
  );
}
