// The Oath emblem, drawn from geometry so it stays crisp at any size and
// themes with the surface it sits on. The medallion is filled in `currentColor`
// (cream on dark, ink on cream); the dividing gaps are stroked in `--logo-bg`,
// which each surface sets to its own background color. Six segments around a
// hexagonal hub inside a ring — identity, sealed.

const C = 50;

function polar(r: number, deg: number): [number, number] {
  const a = (deg * Math.PI) / 180;
  return [C + r * Math.cos(a), C - r * Math.sin(a)];
}

// Pointy-top hexagon: spokes at 90, 150, 210, 270, 330, 30 degrees.
const SPOKES = [90, 150, 210, 270, 330, 30];
const HUB_R = 14;
const RING_INNER = 37;

function hexPath(r: number): string {
  const pts = SPOKES.map((d) => polar(r, d));
  return "M" + pts.map(([x, y]) => `${x.toFixed(2)} ${y.toFixed(2)}`).join(" L ") + " Z";
}

export function Logo({
  size = 44,
  className,
  title = "Oath Language",
}: {
  size?: number;
  className?: string;
  title?: string;
}) {
  const gap = 3.1;
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 100 100"
      className={className}
      role="img"
      aria-label={title}
      fill="none"
    >
      {/* the full cream medallion */}
      <circle cx={C} cy={C} r={43} fill="currentColor" />
      {/* carve the ring / pattern boundary */}
      <circle
        cx={C}
        cy={C}
        r={RING_INNER}
        fill="none"
        stroke="var(--logo-bg, #0a0b09)"
        strokeWidth={gap}
      />
      {/* carve the hexagonal hub */}
      <path d={hexPath(HUB_R)} fill="none" stroke="var(--logo-bg, #0a0b09)" strokeWidth={gap} />
      {/* six spokes dividing the annulus into segments */}
      {SPOKES.map((deg, i) => {
        const [x1, y1] = polar(HUB_R - 0.5, deg);
        const [x2, y2] = polar(RING_INNER + 0.5, deg);
        return (
          <line
            key={i}
            x1={x1}
            y1={y1}
            x2={x2}
            y2={y2}
            stroke="var(--logo-bg, #0a0b09)"
            strokeWidth={gap}
          />
        );
      })}
    </svg>
  );
}

export function Wordmark({
  size = 44,
  showTagline = false,
  className,
}: {
  size?: number;
  showTagline?: boolean;
  className?: string;
}) {
  return (
    <span className={`wordmark ${className ?? ""}`}>
      <Logo size={size} />
      <span className="wordmark-text">
        <span className="wordmark-name">OATH</span>
        {showTagline && (
          <span className="wordmark-tag">Verified code. Immutable truth.</span>
        )}
      </span>
      <span className="wordmark-lang">Lang</span>
    </span>
  );
}
