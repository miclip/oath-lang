# #115 — what real deliveries actually look like

**Status: MEASURED.** Three preceding documents named the same gap and none
could close it: `issue-115-premise.md`, `issue-115-consumer.md` and
`issue-115-composition.md` all measured a **409,600-byte** synthetic body and
all recorded that no real delivery had been sampled. This samples some.

**Nothing is proposed here and nothing was built.** This is a distribution, and
the one figure that matters is not the one the earlier documents expected.

## Provenance, and what it is not

The payloads are the published examples in
[`octokit/webhooks`](https://github.com/octokit/webhooks) under
`payload-examples/api.github.com/`, copied into
`docs/experiments/issue-115-payloads/` so this measurement does not depend on a
network fetch. That repository generates its webhook TYPES from these, so they
are real in shape and field population rather than hand-written illustrations.

**They are not captured traffic, and three biases follow.** They are one or a few
examples per event rather than a sample weighted by how often each event fires;
they are drawn from small demonstration repositories, so arrays that grow with
repository size are near their floor; and — most importantly — **all three
`push` examples carry ZERO commits**, which is the size floor rather than near
it. The `commits` array sits after `repository` and GitHub caps it at 2,048
entries, so a real push is both LARGER and has a SMALLER `repository`
percentage than anything below. This corpus is therefore biased toward small
bodies, the bias is not uniform across events, and for `push` it is extreme.

## The measurement

Both variables jointly, which `issue-115-consumer.md` established is the only
form that can rank anything here: receipt saving scales with TOTAL BYTES, the
traversal penalty with the OFFSET of the scanned field.

| event | n | median bytes | median offset | median % |
|---|---|---|---|---|
| `create` | 3 | 6141 | 102 | 1.7% |
| `delete` | 3 | 6147 | 58 | 0.9% |
| `fork` | 2 | 11170 | 5100 | 45.7% |
| `issue_comment` | 3 | 13288 | 7278 | 52.0% |
| `issues` | 3 | 12645 | 6558 | 51.9% |
| `pull_request` | 3 | 24621 | 18532 | 75.3% |
| `push` | 3 | 6573 | 317 | 4.8% |
| `release` | 3 | 7815 | 1728 | 22.3% |
| `star` | 2 | 6059 | 47 | 0.8% |
| `watch` | 2 | 6070 | 20 | 0.3% |

Range across all 27: body 6,032–25,249 bytes; `repository` at 0.3%–75.9%.

Regenerate with `python3 docs/experiments/issue-115-payloads/measure.py`, which
is committed beside the payloads and states its normalisation. The figures
depend on it: the examples are pretty-printed and GitHub delivers compact JSON,
so each is re-serialised without whitespace before measuring.

## The observation is about SIZE, and it is about THIS CORPUS

Every figure in the three preceding documents was taken on a 409,600-byte body.
**The median of these checked-in examples is 7,740 bytes — 53× smaller — and the
largest is 25,249 bytes, still 16× smaller.**

**That is a statement about the Octokit example corpus and not about deliveries
as a class**, for the three bias reasons above and because 27 unweighted
examples chosen to illustrate event shapes are not a sample of traffic. What it
does establish is that the size the earlier work measured at is far outside the
range these examples occupy, which is enough to make the extrapolation question
live without settling where real traffic sits.

If real deliveries do cluster near these sizes, the receipt saving those
documents measured — 48×, 68,950 KiB — has proportionally less to work on,
because it scales with total body bytes. **Whether they do is exactly what is
still unmeasured.**

**The offset spread is real and wide** — `watch` at 0.3%, `pull_request` at
75.3% — which confirms the consumer document's insistence that offset is a
genuine second variable and not a constant. It is simply not the variable that
dominates at these sizes.

## What this does NOT establish

**It does not rank #115, and the arithmetic that would is not run here.**
`issue-115-consumer.md` fitted a two-variable model — receipt saving 0.16482 KiB
per octet of body against a traversal penalty 0.08650 KiB per octet scanned —
and labelled it explicitly as a model rather than a result, noting that it reads
a single body size as a per-byte slope through the origin. **Evaluating it at
7,740 bytes would be extrapolating 53× outside the one size it was fitted at**,
which is the same error that document withdrew a crossover claim for. The
honest next step is to MEASURE at real sizes, not to project to them.

**It does not establish the population.** 27 examples across 10 event types,
biased small for the three reasons above, is not a distribution of what any
particular deployment receives. A receiver that only handles `push` on a busy
repository faces a different distribution from one that handles `issues` on a
quiet one, and neither is this table.

**It says nothing about the composition result.** `issue-115-composition.md`
found the traversal's closure-and-`Bool` machinery is 99.79% of the non-receipt
column at 409,600 bytes and 0.38% of the run at `repo`-first. Both are per-octet
costs; how they land at 7,740 bytes was not measured.

## What would finish it

Re-run the premise and consumer measurements at real payload sizes — 6 KB, 8 KB,
25 KB — rather than extrapolating the 409,600-byte figures down to them. The
harnesses and payload generators for both already exist and are committed; what
changes is the size parameter and the field position, both of which this table
now supplies from real examples rather than from a guess.
