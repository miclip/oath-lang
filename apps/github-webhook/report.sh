#!/bin/sh
# Read the event log. This is the DEPENDENT — the reason the receiver is an
# application rather than an example, and the thing that broke when the record
# format changed.
#
#   ./report.sh <events.log>
#
# Redeliveries are DEDUPLICATED HERE, and that is not a convenience. GitHub
# resends a delivery with the same X-GitHub-Delivery when the operator clicks
# redeliver or when a delivery failed; the receiver is a pure function with no
# state, so it cannot know it has seen one before and cannot be idempotent. The
# log is therefore at-least-once, and every consumer inherits that. Deduplicating
# on the delivery id is the whole of the workaround.
set -eu

log="${1:?usage: report.sh <events.log>}"
schema="oath-gh/1"

# REFUSE A FORMAT WE DO NOT UNDERSTAND. The record gained a field once already
# — the repository — and this script read the old three-field lines without
# complaint, reporting an empty repository for every one of them. Silence was
# the bug. A line whose first field is not the schema tag is not a line this
# script is entitled to interpret.
# THE FIELD COUNT IS PART OF THE SCHEMA. Checking only the tag would let a
# truncated or over-split line carrying `oath-gh/1` through, and the aggregation
# would report an empty or shifted repository — which is the exact silent
# misreading the tag exists to prevent, recreated one field along.
bad=$(awk -F'\t' -v s="$schema" '$1 != s || NF != 5' "$log" | wc -l | tr -d ' ')
if [ "$bad" != "0" ]; then
  echo "refusing: $bad line(s) in $log are not $schema with 5 fields" >&2
  awk -F'\t' -v s="$schema" '$1 != s || NF != 5 { print "  " $0 }' "$log" >&2
  exit 1
fi

# DEDUPLICATE FIRST, then aggregate. Field 2 is the delivery id: what GitHub
# asserted about this event's identity, and the only field a consumer may treat
# as one. Everything downstream reads the deduplicated set — counting distinct
# deliveries in the header while aggregating raw lines below it would report a
# redelivered event twice under a banner saying it had been deduplicated, which
# is worse than not deduplicating at all.
uniq_log=$(mktemp)
trap 'rm -f "$uniq_log"' EXIT INT TERM
awk -F'\t' '!seen[$2]++' "$log" > "$uniq_log"

total=$(wc -l < "$log" | tr -d ' ')
unique=$(wc -l < "$uniq_log" | tr -d ' ')

echo "$log: $total record(s), $unique distinct deliver(ies)"
[ "$total" = "$unique" ] || echo "  ($((total - unique)) redelivered, counted once below)"
echo
echo "by repository and event:"
# Fields are schema, delivery, EVENT, REPOSITORY, received-at — so the columns
# are swapped here to match the heading. `cut -f3,4` prints in file order and
# would have put the event where a reader expects the repository.
awk -F'\t' '{ print $4 "\t" $3 }' "$uniq_log" | sort | uniq -c | sort -rn | sed 's/^/  /'
