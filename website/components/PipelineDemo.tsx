"use client";

import { useEffect, useState } from "react";

const STEPS = [
  { k: "elaborate", v: "s-expr → canonical AST" },
  { k: "hash", v: "SHA-256 of the O1 encoding" },
  { k: "typecheck", v: "structural, no inference" },
  { k: "test", v: "200 seeded cases" },
  { k: "prove", v: "Z3, all inputs" },
];

export function PipelineDemo() {
  const [active, setActive] = useState(0);

  useEffect(() => {
    const id = setInterval(() => {
      setActive((a) => (a + 1) % (STEPS.length + 1));
    }, 1100);
    return () => clearInterval(id);
  }, []);

  return (
    <div className="pipe">
      {STEPS.map((s, i) => (
        <div
          key={s.k}
          className={`pipe-step ${i === active ? "active" : ""} ${i < active ? "done" : ""}`}
        >
          <div className="pipe-k">
            {i < active ? "✓ " : ""}
            {s.k}
          </div>
          <div className="pipe-v">{s.v}</div>
        </div>
      ))}
    </div>
  );
}
