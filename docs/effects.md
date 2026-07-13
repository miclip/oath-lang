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
2. **Linearity / no-escape checking:** a kernel pass (like the termination
   checker: metadata verdict, conservative) proving a capability parameter
   is used but never stored, returned, or captured in a constructor — making
   attenuation and confinement auditable facts, not conventions.
3. **Stateful worlds:** today's tables are pure functions, so they model
   read-only worlds. Sequenced effects (write-then-read) need explicit
   state threading in the function's own signature; a `World`-state
   convention plus generator support for scripted record/replay worlds.
4. **Entry-point wiring:** the eventual compiler backend supplies genuine
   capabilities exactly once, at the program boundary; everything below the
   boundary keeps the property that authority is visible in every signature
   on the path that carries it.

## What this deliberately does not claim

Capability passing does not make effectful code pure, and table worlds do
not model time, concurrency, or failure injection yet. What it preserves is
the project's actual invariant: the spec slice tells the truth about what a
definition can do, and every guarantee attached to it was earned against a
deterministic, reproducible world.
