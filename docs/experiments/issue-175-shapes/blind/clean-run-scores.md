# The clean run — four subjects, no leak disclosed by any of them

Contexts: all four clean of work state. t3 disclosed listing a harness output
DIRECTORY (names/sizes only, nothing about the corpus); no subject ran ps.

| intent | artifact | t3 (guide) | t4 (guide) | c3 (none) | c4 (none) |
|---|---|---|---|---|---|
| 1 | config-missing | yes | **MISS** | yes | yes |
| 2 | header-or | yes | yes | yes | yes |
| 3 | json-string-value | yes | yes | yes | yes |
| 4 | record-field | yes | yes | yes | yes |
| 5 | media-type-is/path-is | yes | yes | yes | yes |
| 6 | any | yes | declined | declined | declined |
| 7 | take-while | yes | yes | yes | yes |
| | **score** | **7** | **5** | **6** | **6** |
| | tool calls | 61 | 70 | 55 | 47 |
| | tokens | 89K | 93K | 71K | 88K |

    TREATMENT   mean score 6.0    mean 65.5 calls    mean 91.0K tokens
    CONTROL     mean score 6.0    mean 51.0 calls    mean 79.5K tokens
    BASELINE    2 of 7

## The result is a NULL, and the effort difference runs the wrong way

The guide made no difference to what was found. Controls used FEWER tool calls
and FEWER tokens. The hypothesis that the documentation lifts the rate is not
supported; the earlier oracle run's 5-of-7 and the first blind pair's 6-7 of 7
are reproduced by subjects who never saw the axes.

## What actually found the artifacts — none of it is in the guide

- **The SIGNATURE PROBE.** A query whose only law is trivially reflexive,
  `(== (wanted x) (wanted x))`, never hash-matches — so `--spec` always falls
  through to "N definition(s) have a COMPATIBLE SIGNATURE" and ENUMERATES the
  corpus at that shape. t3 built seventeen of them and called it "the cheapest
  map of the corpus and what surfaced most of the answers". s2 and c1 invented
  the same trick independently. THE GUIDE DOES NOT DESCRIBE THIS. It only says
  the fallback list is a useful signal when it happens to appear.
- **Corpus enumeration.** c3 read all 238 names from `codebase/names.json`; c4
  and c1 ran `oath ls`. All disclosed it unprompted as possibly biasing them.
- **`oath dependents`.** t3 found `config-missing` through
  `dependents config-key` — not through any find mode at all.

## The uncomfortable part: the guide may NARROW the strategy

t4 is the only subject that missed intent 1, and it is the one that most
faithfully followed the guide's method. It wrote: "I deliberately did not grep
or read the full `ls` name list, `names.json`, or the object store for intent
keywords — every candidate was surfaced by a `find` mode's own output." It then
tried NINETEEN signature shapes for intent 1 and never hit
`(List Str) x (List Str) -> Str`.

c3 and c4 read the names and found it immediately.

The guide frames discovery as law-writing and never mentions that the corpus has
a readable index. That framing is a defect, and it was found by the falsifier the
documentation was written to satisfy.
