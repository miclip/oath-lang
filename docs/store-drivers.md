# Store drivers: lifting the single-instance cap (#14)

The v1 registry is the filesystem store over gcsfuse, pinned to one writer
(`docs/registry-verification.md`, `terraform/README.md`). This is the design for
the backends that remove that cap: **GCS for the immutable objects, Postgres for
the contended mutable state.** It is scoped so the implementation is a driver
change behind a seam, not a kernel rewrite — the higher-level logic (hashing,
JSON, the journal chain, metadata merge, the proof-queue lease) stays in `Store`;
only the byte-level I/O moves behind an interface.

## The seam

Everything `Store` does to the filesystem reduces to a small byte-level surface.
Extract it as an interface; the current code becomes the `fs` implementation.

```go
type backend interface {
    // Content-addressed, immutable. Writing the same hash twice is idempotent
    // (identical bytes), so writes need no coordination and never conflict.
    GetObject(hash string) ([]byte, bool, error)
    PutObject(hash string, b []byte) error
    ListObjects() ([]string, error)

    // Per-hash metadata (mutable, but keyed by hash — cross-hash writes never
    // conflict; same-hash writes are serialized by the mutation lock).
    GetMeta(hash string) ([]byte, bool, error)
    PutMeta(hash string, b []byte) error

    // The name index — the contended mutable state. ReadNames+WriteNames is a
    // read-modify-write that MUST be atomic against concurrent repoints.
    ReadNames() ([]byte, error)
    WriteNames(b []byte) error

    // The journal — append-only, the chain computed in-kernel. ReadJournal
    // returns the exact bytes VerifyLog anchors its byte offsets on.
    ReadJournal() ([]byte, error)
    AppendJournal(line []byte) error

    // The proof queue (lease semantics live here — the fs backend does it with
    // rename+mtime; Postgres with FOR UPDATE SKIP LOCKED).
    EnqueueProof(job []byte, hash string) error
    ClaimProof(now time.Time, ttl time.Duration) ([]byte, bool, error)
    CompleteProof(hash string) error
    ProofDepth() (int, error)

    // The mutation mutex (fs: an O_EXCL lock file; Postgres: pg_advisory_lock).
    Lock() (unlock func(), err error)
}
```

`Store` keeps `hashDef`, the metadata-merge in `StoreObject`, the chain math in
`AppendLog`/`VerifyLog`, and the queue job shape — all backend-agnostic. The rest
of the kernel already goes through `Store` methods, so **no call site outside the
store layer changes.**

## GCS object backend

Objects and meta are content-addressed blobs — the easy, dumb, cheap part.

- `objects/<hash>.bin`, `meta/<hash>.json` as GCS objects. Immutable → no
  versioning, no locking, and an aggressive CDN in front of the read path (the
  `cdn.tf` that v1 must keep off, because v1's bucket also holds mutable files —
  here objects live in their own immutable bucket, so the CDN is finally sound).
- `PutObject` uses `ifGenerationMatch=0` (write-once); a re-put of the same hash
  is a harmless conflict swallowed to success (identical content).
- `ListObjects` is a bucket list (only used by `AllHashes`/`dependents`; can be
  index-backed later if it gets hot).
- Reads need no trusted compute: a client re-verifies by hash, so a public
  objects bucket is sound.

## Postgres backend — the mutable, contended state

One instance, three tables, real transactions.

```sql
create table names   (name text primary key, hash text not null, updated timestamptz);
create table journal (seq bigserial primary key, entry jsonb not null, chain text not null);
create table proofq  (hash text primary key, job jsonb not null, gate bool,
                      leased_at timestamptz);   -- null = available
```

- **Repoint** is a transaction over `names` (`INSERT … ON CONFLICT … DO UPDATE`
  under a row lock) — the read-modify-write the fs lock only *approximates* is
  now genuinely atomic and serializable.
- **AppendJournal**: the chain is still computed in-kernel (SPEC §8), but under a
  transaction that reads the current tail and inserts the next row, so `seq` and
  the chain never fork. `ReadJournal` reconstructs the byte stream in `seq`
  order; VerifyLog is unchanged (it re-derives the chain from those bytes).
- **Proof queue**: `ClaimProof` is `UPDATE proofq SET leased_at=now WHERE hash =
  (SELECT hash FROM proofq WHERE leased_at IS NULL OR leased_at < now-ttl
  ORDER BY gate DESC, seq LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING job` — real
  single-dispatch, gate jobs prioritized, stale leases reclaimed, no busy-wait.
- **Lock**: `pg_advisory_xact_lock` replaces the O_EXCL file for the same short
  critical sections (or drop it entirely where a single SQL transaction already
  gives atomicity — Repoint and AppendJournal no longer need a separate lock).

### The cross-store commit

GCS and Postgres aren't in one transaction, but content addressing removes the
need. A put commits in two steps, ordered so a crash is always safe:

1. `PutObject`/`PutMeta` to GCS (idempotent, content-addressed).
2. One Postgres transaction: repoint the name + append the journal entry.

A failure between them leaves an **orphan object** (addressable, unreferenced) —
harmless and GC-able — but never a dangling name or a torn journal. The name only
moves inside the transaction, which only runs after the bytes are durably stored.

## Lifting the cap

With the mutable state transactional, the serve service drops `min=max=1`:
`min_instance_count = 0`, `max_instance_count = N`. The worker Job scales out the
same way (the queue is now real single-dispatch). `OATH_STORE_LOCK` (the fs
mutex) is unset — Postgres is the coordinator. This is exactly the
`enable_database = true` path the Terraform already gates; wiring the serve
container to the DB (the env the v1 config deliberately omits) is the last step.

## Migration

`oath migrate-store` reads an fs store and populates GCS + Postgres:

1. Every `objects/*` and `meta/*` → GCS (idempotent; re-runnable).
2. `names.json` → `names` rows.
3. `log.jsonl` → `journal` rows **preserving bytes and order**, so the migrated
   journal's chain still verifies end-to-end (the migration asserts
   `VerifyLog` passes against the reconstructed stream before committing).
4. `proofq/*` → `proofq` rows.

Content addressing makes it safe to run repeatedly and to run READS against the
old store while backfilling.

## Conformance

SPEC §8.1's filesystem layout stays the normative reference. A driver is
conformant iff, for the same inputs, it yields byte-identical hashes, verdicts,
and — crucially — a journal whose `VerifyLog` passes and whose chain values match
the fs store's. That's a new conformance dimension: run the existing store tests
(journal chain, tamper detection, put/repoint, proof-queue lifecycle) against
each backend via the interface, plus a differential test fs-vs-driver.

## Testing

The seam makes this tractable without cloud dependencies:

- An **in-memory backend** runs the entire existing store test suite backend-
  agnostically (the fastest guard that the interface is faithful).
- **fake-gcs-server** for the GCS backend; a **Postgres testcontainer** for the
  SQL backend, exercised by the same interface tests.
- A **differential test**: the same sequence of puts/proves/repoints against the
  fs store and a driver must produce identical `oath log`, `names`, and hashes.

## Why staged, not shipped here

The rewire touches the audit trail (`VerifyLog`'s byte-offset anchoring, the
atomic-write path, the queue lease) — the one part of the system that is
explicitly non-regenerable. It deserves the in-memory + testcontainer harness
above as a safety net before it goes near a live journal, which is a focused
effort, not a tail-end refactor. This document is the contract that makes that
effort mechanical.
