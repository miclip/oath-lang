import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// THE ONE MARKDOWN RENDERER FOR THE SITE.
//
// It exists because there were two call sites rendering docs/*.md with plain
// <ReactMarkdown>, and they had to agree about which markdown dialect the
// repository writes. They did not: react-markdown implements CommonMark, which
// has NO TABLES, so every `| a | b |` in docs/ rendered as literal pipe
// characters — 201 such lines in SPEC.md alone, including §14's transformation
// table, which is the normative centre of the handler protocol and the least
// readable thing imaginable as a wall of unaligned pipes.
//
// remark-gfm is what the repository's markdown actually is: it is written to be
// read on GitHub, so tables, strikethrough, task lists and autolinks are all in
// use. Rendering it as CommonMark was a dialect mismatch, not a styling bug.
//
// Both pages now render through here so a third page cannot reintroduce the
// disagreement by copying whichever call site it happened to look at.
export function Markdown({ children }: { children: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        // Tables get a scroll container rather than being allowed to widen the
        // column. `.prose` is 720px and several of these are five columns of
        // prose — SPEC §14.2 row 9 alone is a paragraph. The alternative,
        // `display: block` on the table itself, scrolls but discards the
        // column sizing that makes a table worth having.
        table: ({ children }) => (
          <div className="table-scroll">
            <table>{children}</table>
          </div>
        ),
        // The repository's markdown is written to be read on GitHub, so internal
        // cross-links are repo-relative and end in `.md` (e.g. `discovery.md`).
        // The site routes carry no `.md`, so those links would 404 here. Strip a
        // trailing `.md` (keeping any `#anchor`) from relative links; browser-
        // relative resolution then lands on the right route wherever the site
        // layout mirrors the docs layout — which it does within a section, so a
        // tutorial's `discovery.md` correctly reaches `/docs/tutorials/discovery`.
        // (A cross-SECTION link like a tutorial's `../floats.md` still lands at
        // `/docs/floats`, not `/docs/reference/floats`: the reference route does
        // not mirror `docs/*.md`, a separate pre-existing layout gap.) Absolute
        // URLs, `mailto:`, and bare `#anchors` are left untouched.
        a: ({ href, children }) => {
          let h = href ?? "";
          if (h && !/^([a-z]+:|#|\/\/)/i.test(h)) {
            h = h.replace(/\.md(#.*)?$/i, "$1");
          }
          return <a href={h}>{children}</a>;
        },
      }}
    >
      {children}
    </ReactMarkdown>
  );
}
