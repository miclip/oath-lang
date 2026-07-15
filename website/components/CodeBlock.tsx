import React from "react";

// A small, dependency-free highlighter for Oath's s-expression surface syntax.
// Not a real parser — just enough lexical coloring to read well.

const KEYWORDS = new Set([
  "defn",
  "data",
  "match",
  "prop",
  "if",
  "let",
  "and",
  "or",
  "not",
]);

function highlightLine(line: string, key: number): React.ReactNode {
  const trimmed = line.trimStart();
  if (trimmed.startsWith(";")) {
    return (
      <span key={key} className="tok-comment">
        {line}
        {"\n"}
      </span>
    );
  }

  const nodes: React.ReactNode[] = [];
  // split into strings | parens | words | whitespace | other
  const re = /("(?:[^"\\]|\\.)*")|([()[\]{}])|([^\s()[\]{}"]+)|(\s+)/g;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(line)) !== null) {
    const [full, str, paren, word, ws] = m;
    if (str !== undefined) {
      nodes.push(
        <span key={i++} className="tok-str">
          {str}
        </span>,
      );
    } else if (paren !== undefined) {
      nodes.push(
        <span key={i++} className="tok-paren">
          {paren}
        </span>,
      );
    } else if (ws !== undefined) {
      nodes.push(<React.Fragment key={i++}>{ws}</React.Fragment>);
    } else if (word !== undefined) {
      let cls = "";
      if (KEYWORDS.has(word)) cls = "tok-kw";
      else if (/^[A-Z]/.test(word)) cls = "tok-type";
      else if (word === "==" || word === "<=" || word === "<" || word === ">") cls = "tok-kw";
      // property names appear right after `prop`
      const prev = nodes.length ? full : "";
      void prev;
      if (cls) {
        nodes.push(
          <span key={i++} className={cls}>
            {word}
          </span>,
        );
      } else {
        nodes.push(<React.Fragment key={i++}>{word}</React.Fragment>);
      }
    }
  }
  nodes.push(<React.Fragment key={i++}>{"\n"}</React.Fragment>);
  return <React.Fragment key={key}>{nodes}</React.Fragment>;
}

export function CodeBlock({
  code,
  label,
  verdict,
}: {
  code: string;
  label?: string;
  verdict?: React.ReactNode;
}) {
  const lines = code.replace(/\n$/, "").split("\n");
  return (
    <div className="code">
      {(label || verdict) && (
        <div className="code-head">
          <span>{label ?? "oath"}</span>
          {verdict}
        </div>
      )}
      <pre>
        <code>{lines.map((l, idx) => highlightLine(l, idx))}</code>
      </pre>
    </div>
  );
}
