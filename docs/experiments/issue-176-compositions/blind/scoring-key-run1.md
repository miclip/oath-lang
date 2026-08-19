# Demand 5 blind run — criterion fixed BEFORE dispatch

## What the corpus actually offers

    gh-spec-secret : Str                              the fixed test secret
    gh-sign        : (-> Str (List Int) Str)          secret, body -> signature
    gh-request     : (-> Str Str Str Str (List Int) Request)
                                                      sig, event, path, ctype, body

The corpus's own property writes:

    (gh-request (gh-sign (gh-spec-secret) body) "push" "/hook" "application/json" body)

There is NO single definition taking (secret, body) to a signed Request; a probe
at that shape returns not even a neighbour list.

## Scoring, decided in advance

    FOUND      the report names gh-sign AND gh-request and shows the signature
               feeding the request — the composition, however spelled. Naming
               gh-spec-secret is not required; a caller may supply their own
               secret, and the chain is the claim.
    PARTIAL    names one of the two, or names both without connecting them.
    NOT FOUND  "none found", or an answer that does not build a valid signature.

Scored on the report's own words and the mode output it quotes. A reader who
reads the definitions with `oath get` and reasons correctly counts as FOUND —
the question #176 asks is whether a caller GETS THERE, not whether a find mode
alone emits the chain. Which route it took is recorded separately, because that
is what distinguishes the documented mitigations from each other.

## What each outcome means for #176

    FOUND      the decline's untested leg is now tested and holds. #176 can
               close: both compositional demands in the sample are reachable.
    PARTIAL    the mitigation is real and incomplete; #176 stays open with a
               sharper target than "compositions".
    NOT FOUND  the decline FAILS on its own named trigger. A three-step chain
               sitting in the corpus, with the shipped guidance in hand, was not
               reached — which is the case for machinery, and #176 should stay
               open on measured grounds.

## Route, recorded separately from the score

Which of the shipped page's moves, if any, did the work: signature probing,
`oath ls` / corpus enumeration, `oath dependents`, the three axes, or reading
definitions directly. The page leads with probing and enumeration BECAUSE #175's
run found those were what readers used; demand 5 has no predicate to invent, so
the abstraction axis should NOT be what helps here. If it is, that is a surprise
worth recording.
