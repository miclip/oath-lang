# Generated subgoal scripts — `opt.preserves` structural induction

The two SMT-LIB scripts the Oath prover itself emits for the structural-induction
strategy on `opt.preserves`, exported BYTE-FOR-BYTE. They are the prover's output,
not a reconstruction: nothing here re-derives the bytes.

| file | strategy detail | constructor | bytes | SHA-256 |
|---|---|---|---|---|
| `opt-preserves-induction-binder0-ctor1-Add.smt2` | `binder 0 ctor 1` | `Add` | 2534 | `656e7fa708565e9fb9fa8394f69b3f725ca14b0f0aad82ba8fee08ad1105a371` |
| `opt-preserves-induction-binder0-ctor2-Mul.smt2` | `binder 0 ctor 2` | `Mul` | 2534 | `b7ffb6a2e739a67d82335727668f2c98e7efe019c9b7c35b409a5096c10709af` |

Inputs, for reproduction:

| artifact | SHA-256 |
|---|---|
| `../optimizer.oath` | `6915910ae2aec2589a619b8490aae824ad90eccd3e3576df564da2d1aa583d7e` |
| `extract-instrumentation.go.txt` | `688866a1f0f2108bc0b34f57be333303a44703602a29047667acdcb1f05d9ddb` |

`opt` object hash: `4aa5368897539e97dcffb6df46c9ab36fb45a07192314b466ee193f8c1c85b78`
Kernel commit: `fa96464a6428900f1978fc849470bcd7d1a39e68` · z3 4.16.0 · go1.25.6 darwin/arm64

## Constructor labels are the kernel's, not assumed

The `Add`/`Mul` labels are read off the prover's own datatype table, printed by the
instrumentation, NOT inferred from declaration order in the source:

    CTORS   Expr    [Lit_Expr Add_Expr Mul_Expr]

so ctor 0 = `Lit`, ctor 1 = `Add`, ctor 2 = `Mul`.

## Extraction

The shared `oath/` tree was NOT edited. A throwaway copy of the module was made, and
one new test file added to it (`extract-instrumentation.go.txt`, verbatim copy). It
adds no behaviour: it calls the kernel's existing `scriptAttempts` seam (prove.go:333),
which enumerates the scripts the strategy sequence can emit for one property under the
recorded lemma state, WITHOUT running a solver.

    # 1. throwaway copy of the module; add the instrumentation as a test file
    cp -R oath /tmp/oathinst && rm -f /tmp/oathinst/oath
    cp docs/experiments/optimizer-consumer/generated-subgoals/extract-instrumentation.go.txt \
       /tmp/oathinst/zz_export_subgoals_test.go
    (cd /tmp/oathinst && go build -o oathinst .)

    # 2. recreate the store exactly as run.sh does: put, prove both soundness
    #    lemmas under the same cap, admit both hints
    export OATH_STORE=$(mktemp -d)
    cap() { OATH_PROVE_RLIMIT=15000000 OATH_PROVE_WALLCAP_SEC=120 OATH_PROVE_MEMORY_MB=3000 "$@"; }
    /tmp/oathinst/oathinst put docs/experiments/optimizer-consumer/optimizer.oath --new
    cap /tmp/oathinst/oathinst prove add-s
    cap /tmp/oathinst/oathinst prove mul-s
    /tmp/oathinst/oathinst hint opt preserves add-s.sound
    /tmp/oathinst/oathinst hint opt preserves mul-s.sound

    # 3. enumerate and write every emitted script
    (cd /tmp/oathinst && \
      OATH_STORE=$OATH_STORE SUBGOAL_OUT=/tmp/raw SUBGOAL_DEF=opt SUBGOAL_PROP=preserves \
      go test -run TestZZExportSubgoals -v .)

    # 4. attempts 03 and 04 are the two induction subgoals
    shasum -a 256 /tmp/raw/attempt-03.smt2 /tmp/raw/attempt-04.smt2

Store state at extraction, matching run.sh: `add-s.sound` and `mul-s.sound` both
`∎ PROVEN direct (lemma-free)`, and both hints `1 lemma admitted`.

## The full emitted sequence

`scriptAttempts` reports six scripts for `opt.preserves`, in emission order:

    0  lemma-free                          1768 bytes
    1  direct                              2370 bytes
    2  induction  binder 0 ctor 0  (Lit)   2415 bytes
    3  induction  binder 0 ctor 1  (Add)   2534 bytes   <- exported
    4  induction  binder 0 ctor 2  (Mul)   2534 bytes   <- exported
    5  direct-fallback                     2370 bytes

## Two observations about the exported bytes

**The Add and Mul scripts differ in exactly two tokens.** A whitespace-token diff
shows only the constructor in the negated goal (`Add_Expr` vs `Mul_Expr`, twice).
Both carry identical premises: the two defining equations (`fn_ev`, `fn_opt`), the
two hinted soundness lemmas as quantified asserts, and BOTH induction hypotheses
`(= (fn_ev (fn_opt f0)) (fn_ev f0))` and the same for `f1`.

**`(declare-const b0 Expr)` is unused in the induction scripts.** In attempts 0, 1
and 5 the binder const `b0` is declared and used; in the three induction scripts
(2, 3, 4) it is declared and never referenced — the goal is over the fresh field
consts `f0`/`f1` instead. An unconstrained declaration cannot change satisfiability,
so this is inert, but it is a vestigial declaration rather than an intentional one.
NOT filed as a defect: it is recorded here only because these bytes are now pinned,
and a later cleanup would change both hashes above.

## Lemma-state dimension — MEASURED, not assumed

`scriptAttempts` is scoped to ONE lemma state (the recorded/settled one), leaving the
lemma-state dimension open by design (see its doc comment and SPEC §7.2). That
residue does NOT bite here, and it was checked rather than reasoned about: the
enumeration was run twice, once in the state above and once in a copy of that store
after `oath prove opt` had also been run, and ALL SIX scripts were byte-identical.
The reason is that `prove opt` proves nothing (`proven: 0/1 properties`), so no
self-lemma is added and the state does not advance. The exported bytes are therefore
not an artifact of when the snapshot was taken.

## The extraction command above was RUN, not just written

The steps in **Extraction** were executed verbatim into a fresh module copy and a
fresh empty store, and both files reproduced byte-for-byte (`cmp` clean, hashes
equal to the table above). The command block is therefore verified reproducible on
this host, not merely a transcription of what was done the first time.

What that does NOT establish: reproduction on a different z3 build. The scripts are
emitted without invoking a solver, so z3 cannot affect their bytes — but z3 4.16.0
IS what proved `add-s.sound` and `mul-s.sound`, and if those proofs failed on
another build the hints would have nothing to admit and the lemma asserts would be
absent from the scripts. So the pinned bytes depend on the two soundness proofs
SUCCEEDING, not on how they succeeded.
