import outcomes from "./outcomes.json";

// ---------------------------------------------------------------------------
// The corpus data on this site is REAL: `outcomes.json` is copied verbatim
// from `fixtures/prove/outcomes.json` in the Oath repo — the machine-generated
// proof ledger the kernel writes when it runs `oath prove` over every example.
// Hashes are the actual SHA-256 content identities; verdicts are the actual
// Z3 outcomes. Nothing here is mocked.
// ---------------------------------------------------------------------------

export type Level = "proven" | "tested" | "falsified" | "asserted";

export interface PropOutcome {
  name: string;
  proven: boolean;
}

export interface Definition {
  name: string;
  hash: string;
  level: Level;
  proven_count: number;
  prop_count: number;
  props: PropOutcome[];
}

const raw = outcomes as { definitions: Definition[] };
export const definitions: Definition[] = raw.definitions;

export const stats = {
  definitions: definitions.length,
  properties: definitions.reduce((n, d) => n + d.prop_count, 0),
  proven: definitions.reduce((n, d) => n + d.proven_count, 0),
  fullyProven: definitions.filter((d) => d.level === "proven").length,
  falsified: definitions.filter((d) => d.level === "falsified").length,
};

export function byName(name: string): Definition | undefined {
  return definitions.find((d) => d.name === name);
}

// A coarse category per definition, for grouping in the explorer.
const CATEGORY: Record<string, string> = {};
const put = (names: string[], cat: string) => names.forEach((n) => (CATEGORY[n] = cat));
put(["length", "append", "map", "reverse", "sum", "contains", "drop", "take", "count", "lengths"], "Lists");
put(["is-sorted", "insert", "sort", "merge"], "Sorting");
put(["t-insert", "t-member", "t-size", "t-flatten"], "Trees");
put(["q-push", "q-peek", "q-drop", "q-to-list"], "Queue");
put(["kv-get", "kv-put", "rename-key", "safe-get", "stash", "leak"], "Worlds & state");
put(["i-contains", "i-overlaps", "i-intersect", "i-hull"], "Intervals");
put(["full-name", "greet", "greet-or-guest", "shout", "join-with", "initials-or", "or-else"], "Strings & records");
put(["Str", "str-len", "str-append", "str-prefix", "str-take", "str-drop", "str-split", "str-join", "str-split-join"], "Strings & records");
put(["abs", "sign", "clamp", "max2", "e-div", "e-mod"], "Numbers");
put(["rat-add", "rat-mul", "rat-recover"], "Rationals");
put(["f-mul-id", "f-double", "f-tenths"], "Floats");
put(["int-embed", "rat-floor", "embed-add", "tenth-f"], "Numeric conversions");
put(["main-echo", "main-fetch", "rot", "rot-f", "rot-h2", "rot-h3", "rot-hl"], "Programs & capabilities");
put(["rle-encode", "rle-decode", "rle-expand"], "Run-length coding");
put(["i-contains", "i-overlaps"], "Intervals");
put(["bad-reverse", "abs-small", "spin"], "Honest exhibits");

export function categoryOf(name: string): string {
  return CATEGORY[name] ?? "Other";
}

// Curated detail for flagship definitions: real source (from the example
// files) plus the one-line story of why each is interesting.
export interface Featured {
  name: string;
  title: string;
  blurb: string;
  source: string;
  notes?: string[];
}

export const featured: Record<string, Featured> = {
  sort: {
    name: "sort",
    title: "Insertion sort, fully proven correct",
    blurb:
      "Authored against the spec of List/length/append — never their bodies. The permutation oath (count preserved) is the strong property: sorted + same-length can both hold for wrong code; sorted + counts-preserved cannot.",
    source: `(defn sort [] [(xs (List Int))] (List Int)
  (match xs
    ((Nil) (Nil [Int]))
    ((Cons h t) (insert h (sort t))))
  (prop output-is-sorted [(xs (List Int))]
    (is-sorted (sort xs)))
  (prop preserves-length [(xs (List Int))]
    (== (length [Int] (sort xs)) (length [Int] xs)))
  (prop preserves-counts [(x Int) (xs (List Int))]
    (== (count x (sort xs)) (count x xs)))
  (prop idempotent [(xs (List Int))]
    (== (sort (sort xs)) (sort xs)))
  (prop sorted-is-fixpoint [(xs (List Int))]
    (if (is-sorted xs) (== (sort xs) xs) true)))`,
    notes: [
      "Proven by structural induction, discharged to Z3 over unbounded integers.",
      "idempotent and reverse-invariant go through a four-lemma plan: insert commutativity, the sorted-head no-op, snoc-is-insert, and the sorted-fixpoint theorem.",
    ],
  },
  reverse: {
    name: "reverse",
    title: "reverse (reverse xs) == xs",
    blurb:
      "The classic involution — proven for all lists via the append laws and reverse's own antidistribution lemma, composing bottom-up through the hash graph.",
    source: `(defn reverse [a] [(xs (List a))] (List a)
  (match xs
    ((Nil) (Nil [a]))
    ((Cons h t) (append [a] (reverse [a] t) (Cons [a] h (Nil [a])))))
  (prop involution [(xs (List Int))]
    (== (reverse [Int] (reverse [Int] xs)) xs))
  (prop antidistributes-over-append [(xs (List Int)) (ys (List Int))]
    (== (reverse [Int] (append [Int] xs ys))
        (append [Int] (reverse [Int] ys) (reverse [Int] xs)))))`,
    notes: [
      "Proven properties become a lemma library: append's laws are asserted as axioms when reverse is proven.",
    ],
  },
  greet: {
    name: "greet",
    title: "Effects as capabilities, quantified over all worlds",
    blurb:
      "A capability is a record of functions passed as an ordinary parameter — the signature is the authority audit. Properties quantify over generated simulated worlds; the kernel also proves the net capability is confined (never returned, stored, or captured).",
    source: `(defn greet [] [(net {fetch (-> Str Str)}) (id Str)] Str
  (str-append "Hello, " (str-append ((. net fetch) id) "!"))
  (prop same-world-same-answer [(net {fetch (-> Str Str)}) (id Str)]
    (== (greet net id) (greet net id)))
  (prop never-shorter-than-frame [(net {fetch (-> Str Str)}) (id Str)]
    (<= 8 (str-len (greet net id)))))`,
    notes: [
      "Capability verdict: net: confined. Contrast examples/leaky.oath, where net: ESCAPES.",
      "Function values are array-encoded for Z3, so higher-order properties quantify over all functions.",
    ],
  },
  "bad-reverse": {
    name: "bad-reverse",
    title: "A wrong reverse, caught honestly",
    blurb:
      "A reverse that returns its input unchanged. Its involution property passes (a lesson about weak specs) — but the antidistribution law falsifies it, and the kernel records a counterexample.",
    source: `(defn bad-reverse [a] [(xs (List a))] (List a)
  xs
  (prop involution [(xs (List Int))]
    (== (bad-reverse [Int] (bad-reverse [Int] xs)) xs))
  (prop antidistributes-over-append [(xs (List Int)) (ys (List Int))]
    (== (bad-reverse [Int] (append [Int] xs ys))
        (append [Int] (bad-reverse [Int] ys) (bad-reverse [Int] xs)))))`,
    notes: [
      "Verdict: FALSIFIED (antidistributes-over-append). oath build refuses to compile a falsified oath.",
      "Counterexample: xs = [-13, -18, 2], ys = [-6].",
    ],
  },
  spin: {
    name: "spin",
    title: "Non-termination, converted to a verdict",
    blurb:
      "The type system accepts it; the termination checker refuses to bless it (the recursion never descends). Rather than reject or hang, the fuel bound turns the infinite loop into an honest FALSIFIED verdict.",
    source: `(defn spin [] [(x Int)] Int
  (spin x)
  (prop claims-zero [(x Int)]
    (== (spin x) 0)))`,
    notes: [
      "Verdict: termination unproven; claims-zero FALSIFIED (runtime error: recursion too deep).",
    ],
  },
  "abs-small": {
    name: "abs-small",
    title: "Tested green, refuted by proof",
    blurb:
      "The exhibit for why the guarantee rungs differ: its property passes all 200 generated test cases, then Z3 refutes it at an input the generator never draws.",
    source: `(defn abs-small [] [(x Int)] Int
  (if (< x 0) (- 0 x) x)
  (prop bounded-wrongly [(x Int)]
    (< (abs-small x) 401)))`,
    notes: [
      "Passes 200/200 test cases; refuted by Z3 at x = -401. Tested is not proven.",
    ],
  },
  length: {
    name: "length",
    title: "Spec strength, measured by mutation",
    blurb:
      "non-negativity alone can't tell length apart from (length + k) or from const 0. Mutation testing scored the original spec 1/5; two anchor properties — a base case and a step law — took it to 5/5.",
    source: `(defn length [a] [(xs (List a))] Int
  (match xs
    ((Nil) 0)
    ((Cons h t) (+ 1 (length [a] t))))
  (prop non-negative [(xs (List Int))]
    (<= 0 (length [Int] xs)))
  (prop empty-is-zero [(x Int)]
    (== (length [Int] (Nil [Int])) 0))
  (prop cons-adds-one [(x Int) (xs (List Int))]
    (== (length [Int] (Cons [Int] x xs)) (+ 1 (length [Int] xs)))))`,
    notes: [
      "mutate is the answer to “who verifies the specs?” Survivors are printed with their bodies.",
    ],
  },
};

export const featuredOrder = [
  "sort",
  "reverse",
  "greet",
  "length",
  "bad-reverse",
  "spin",
  "abs-small",
];
