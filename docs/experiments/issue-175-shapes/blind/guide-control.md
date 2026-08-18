# Phrasing a query for `oath find`

`oath find` discovers proven code by what it PROVES, not by name. Four modes:

    oath find <name>              by example — needs a name
    oath find --spec <file>       does any definition state this law?
    oath find --implies <file>    does any definition PROVABLY satisfy this law?
    oath find --equiv <name>      body-equivalence — needs a name

A query file is an ordinary definition with a dummy body and one or more `prop`
laws. `self` is the definition being queried, so the law is portable:

    (defn wanted [] [(x Int)] Int 0
      (prop some-law [(x Int)] (== (wanted x) x)))

