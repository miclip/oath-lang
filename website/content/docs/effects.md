# RFC: Effects in Oath — capabilities, not an effect system

Status: accepted design, v0 slice implemented (simulated-world generation).

## The problem

Pure total functions are the friendly case. Real software talks to networks,
disks, clocks, and databases — and that is where locality, authority,
replayability, and audit semantics either become real or collapse. The
constraint that must survive contact with effects: **a definition's spec
slice remains a sufficient interface** — an agent building on `fetch-user`
must learn everything relevant from its signature and properties, including
what parts of the world it can touch.

## Options considered

1. **Effect rows** (Koka-style: `(-> A B ! {net, fs})`). Precise, but adds a
   second type system — row polymorphism, effect subsumption, inference
   pressure — to a kernel whose entire value is being small enough to audit.
2. **Monadic IO** (Haskell-style). Composition ceremony infects every
   signature; the kernel gains bind/return laws to trust; and the AI-native
   argument for it (explicitness) is delivered more cheaply by option 3.
3. **Capability passing** (object-capability discipline): an effect is the
   ability to call a function you were *handed*. A capability is an ordinary
   record of functions: `{fetch (-> Str Str)}` is a network you can query.

Decision: **option 3**. The deciding argument is kernel weight: Oath already
has records and higher-order functions, so capability passing adds **zero
type rules** to the trusted core. Everything below is discipline and tooling
on top of machinery that already exists.

## How it works

- **Authority is a parameter.** A function that needs the network takes the
  network: `(defn fetch-user [] [(net {fetch (-> Str Str)}) (id Str)] ...)`.
  No ambient authority exists: kernel primitives never perform IO, so the
  *only* way to affect the world is through something you were passed.
- **Purity is visible.** No capability parameters ⇒ pure. The signature is
  the audit; a spec slice shows exactly what a definition can reach.
- **Attenuation is just wrapping.** A read-only database is a record with
  fewer fields; a logged network is a record whose fields wrap another's.
  Building these requires no kernel features.
- **Verification quantifies over worlds.** Properties bind capability
  parameters like any other input: `(prop p [(net {fetch (-> Str Str)})
  (id Str)] ...)`. The generator supplies *simulated worlds* — records of
  finite table functions (input→output pairs plus a default), fully
  deterministic under the definition-hash seed. Effectful-style code is
  tested with the same machinery, honesty, and reproducibility as pure code.

## What the v0 slice ships

Generated function values are now finite tables (previously only
constant/identity/affine), which makes generated capability records
behaviorally rich enough to falsify real mistakes. `examples/service.oath`
demonstrates the pattern end to end.

## Staged roadmap

1. **Now (shipped):** capability convention + simulated-world generation.
   Capabilities are unforgeable only by discipline — nothing stops a def
   from storing one in a data structure and using it later.
2. **No-escape checking — SHIPPED.** A kernel pass (like the termination
   checker: conservative metadata verdict, never a rejection) proves a
   higher-order parameter is only exercised — applied, projected-and-applied,
   passed recursively at the same position, or passed whole to a callee
   position already verdicted confined — and never returned, stored, or
   captured in an inner lambda. `greet-or-guest` proves confined
   *compositionally* through `greet`'s verdict; `examples/leaky.oath` shows
   the brands (`net: ESCAPES`) for returning or stashing a capability.
   Closure tracking (issue #10) is in: a lambda passed to a callee position
   already verdicted confined is only ever invoked during the call, so
   capability use inside it follows the normal rules — the wrapper idiom
   `(map (fn [u] ((. net fetch) u)) urls)` is confined, while a closure that
   returns the capability (the callee keeps its RESULTS) still escapes.
   Applications must also yield data: a stored partial application of a
   curried capability is a derived closure containing the capability, and
   escapes. Remaining conservatism: a closure passed to a confined position
   of the capability ITSELF (no metadata to consult) counts as an escape.
3. **Stateful worlds — SHIPPED, as a pattern rather than a feature.** The
   design question ("a World-state convention the generator understands vs
   per-capability state machines") resolved by rejecting both: generated
   opaque transition functions produce *lawless worlds* — a random `get`
   table owes nothing to `put`, so sequenced logic would be verified
   against incoherent physics. Instead: **state is data, transitions are
   code.** A world is an explicit ADT value threaded through pure
   functions; the existing generator therefore quantifies over every
   reachable world shape with zero new machinery, the laws of the world
   (read-your-writes, frame, overwrite) are ordinary properties — and
   PROVEN ones, by induction where needed — and failure injection is a sum
   type over the world (`Flaky = Up KV | Down`). `examples/stateful.oath`
   is the worked pattern: a key-value world, client code composing it, and
   an unreliable wrapper, 9/9 properties proven for all worlds. What
   remains genuinely open at this stage: modeling time and interleaving
   (concurrency) — a world value serializes one history.
4. **Entry-point wiring — SHIPPED.** `oath build` compiles capability-first
   entry points ((-> {caps} (List Str) Str)) and wires GENUINE
   implementations exactly once, at the program boundary: `fetch` becomes a
   real HTTP GET, `env`/`readfile` real host access. Everything below the
   boundary received authority as an ordinary argument and was verified
   against all simulated worlds before the real one arrived. The compiler
   refuses unwireable capability fields, refuses falsified or unverified
   entries, and refuses a capability parameter the confinement checker
   marks ESCAPES — a program that stores or returns its capability never
   receives the real one. The corpus witness is `main-fetch`: PROVEN 3/3
   over all worlds, then run against a live HTTP server.

## What this deliberately does not claim

Capability passing does not make effectful code pure, and table worlds do
not model time, concurrency, or failure injection yet. What it preserves is
the project's actual invariant: the spec slice tells the truth about what a
definition can do, and every guarantee attached to it was earned against a
deterministic, reproducible world.
