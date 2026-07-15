"use client";

import { useMemo, useState } from "react";
import {
  definitions,
  featured,
  categoryOf,
  type Definition,
  type Level,
} from "@/lib/corpus";
import { CodeBlock } from "./CodeBlock";

const LEVELS: { key: Level; label: string }[] = [
  { key: "proven", label: "proven" },
  { key: "tested", label: "tested" },
  { key: "falsified", label: "falsified" },
];

function levelLabel(d: Definition): string {
  if (d.level === "proven") return "proven · total";
  if (d.level === "falsified") return "falsified";
  return "tested";
}

function HashRow({ hash }: { hash: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div
      className="pg-hash"
      role="button"
      tabIndex={0}
      title="Click to copy"
      onClick={() => {
        navigator.clipboard?.writeText(hash).then(
          () => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1400);
          },
          () => {},
        );
      }}
      style={{ cursor: "pointer" }}
    >
      <span className="k">sha256</span>
      <span>{copied ? "copied to clipboard ✓" : hash}</span>
    </div>
  );
}

export function CorpusExplorer() {
  const [q, setQ] = useState("");
  const [active, setActive] = useState<Set<Level>>(new Set());
  const [selected, setSelected] = useState<string>("sort");

  const toggle = (lvl: Level) => {
    setActive((prev) => {
      const next = new Set(prev);
      if (next.has(lvl)) next.delete(lvl);
      else next.add(lvl);
      return next;
    });
  };

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return definitions
      .filter((d) => (active.size === 0 ? true : active.has(d.level)))
      .filter(
        (d) =>
          needle === "" ||
          d.name.toLowerCase().includes(needle) ||
          categoryOf(d.name).toLowerCase().includes(needle),
      )
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [q, active]);

  const sel = definitions.find((d) => d.name === selected) ?? filtered[0];
  const feat = sel ? featured[sel.name] : undefined;

  return (
    <>
      <div className="pg-controls">
        <input
          className="pg-search"
          placeholder="search definitions or categories…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <div className="pg-filters">
          {LEVELS.map((l) => (
            <button
              key={l.key}
              className={`pg-filter ${active.has(l.key) ? "on" : ""}`}
              onClick={() => toggle(l.key)}
            >
              <span className={`dot dot-${l.key}`} style={{ display: "inline-block", marginRight: 7 }} />
              {l.label}
            </button>
          ))}
        </div>
      </div>

      <div className="pg-layout">
        <div className="pg-list">
          {filtered.length === 0 && <div className="pg-empty">no matches</div>}
          {filtered.map((d) => (
            <div
              key={d.name}
              className={`pg-row ${sel?.name === d.name ? "sel" : ""}`}
              onClick={() => setSelected(d.name)}
            >
              <div style={{ display: "flex", alignItems: "center", gap: 11 }}>
                <span className={`dot dot-${d.level}`} />
                <span className="pg-row-name">{d.name}</span>
              </div>
              <span className="pg-row-cat">{categoryOf(d.name)}</span>
            </div>
          ))}
        </div>

        <div className="pg-detail">
          {sel && (
            <>
              <div className="pg-detail-head">
                <div>
                  <h2>{feat ? feat.title : sel.name}</h2>
                  {feat && (
                    <div
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 13,
                        color: "var(--cream-faint)",
                        marginTop: 4,
                      }}
                    >
                      {sel.name}
                    </div>
                  )}
                </div>
                <span className={`rung-mark v-${sel.level}`}>{levelLabel(sel)}</span>
              </div>

              <HashRow hash={sel.hash} />

              {feat && <p className="pg-blurb">{feat.blurb}</p>}

              {feat && <CodeBlock label={`${sel.name}`} code={feat.source} />}

              <div className="pg-props-h">
                {sel.proven_count} / {sel.prop_count} properties proven ·{" "}
                {categoryOf(sel.name)}
              </div>
              {sel.props.map((p) => (
                <div className="pg-prop" key={p.name}>
                  <span className={`mark ${p.proven ? "v-proven" : sel.level === "falsified" ? "v-falsified" : "v-tested"}`}>
                    {p.proven ? "proven" : sel.level === "falsified" ? "refuted" : "tested"}
                  </span>
                  <span>{p.name}</span>
                </div>
              ))}

              {feat?.notes && (
                <ul className="pg-notes">
                  {feat.notes.map((n, i) => (
                    <li key={i}>{n}</li>
                  ))}
                </ul>
              )}

              {!feat && (
                <p style={{ color: "var(--cream-faint)", fontSize: 13, marginTop: 24 }}>
                  Every property above was run by the kernel; those marked{" "}
                  <em>proven</em> were discharged to Z3 for all inputs. Hash is the
                  real content identity from the committed store.
                </p>
              )}
            </>
          )}
        </div>
      </div>
    </>
  );
}
