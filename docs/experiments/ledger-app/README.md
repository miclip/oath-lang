# ledger — a double-entry ledger checker

The second real dependent of the kernel, after `apps/github-webhook` (#120).
Built as a "depend on Oath, don't improve it" exercise; the demand list it
produced is `docs/experiments/ledger-friction.md`.

It reads a file of `account amount` postings (one per line, amount a signed
integer of minor units), accumulates per-account balances in **exact ℤ**, prints
them, and **refuses** — with a legible failing exit — any ledger whose postings
do not net to zero. Money that cannot round, and an imbalance that cannot hide.

## Build and run

`ledger.oath` is not yet a committed corpus member (see the friction log for why
that is a deliberate, separate step). Build it against a store that holds the
standard library — e.g. a copy of `codebase/`:

```sh
store=$(mktemp -d); cp -r codebase "$store"
OATH_STORE="$store" oath put docs/experiments/ledger-app/ledger.oath
OATH_STORE="$store" oath build ledger-main -o ledger --backend go   # or --backend llvm
```

Both backends produce byte-identical behaviour.

```sh
$ printf 'cash 1500\nrevenue -1500\nrent -800\ncash 800\n' > book.txt
$ ./ledger book.txt ; echo "exit $?"
cash: 2300
rent: -800
revenue: -1500
exit 0

$ printf 'cash 1500\nrevenue -1400\n' > bad.txt
$ ./ledger bad.txt ; echo "exit $?"
ledger does not balance; net = 100      # on stderr
exit 1

$ ./ledger ; echo "exit $?"
usage: ledger <file>
exit 2
```

## The point

`balances` carries a **proof obligation** — `sum(balances ps) == total(ps)`, that
the accumulation neither creates nor loses value. It currently proves only up to
`tested` (the friction log explains why, and what would fix it), but the claim is
in the artefact's identity either way: a change that dropped or double-counted a
posting would break the property, not just a test.
