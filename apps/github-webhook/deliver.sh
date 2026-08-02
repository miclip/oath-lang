#!/bin/sh
# Deliver a webhook the way GitHub delivers one.
#
# This script shares NO code with Oath. It computes the HMAC with openssl and
# speaks HTTP with curl, exactly as GitHub's documented scheme describes it:
#
#   https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
#
# That independence is the point. If the receiver and the sender were both
# written here, agreement would prove that one implementation is
# self-consistent. openssl computing the digest and Oath verifying it is a real
# cross-implementation check of hmac-sha256, and it is how the receiver's
# hex-vs-raw-bytes bug would have been caught on the first request instead of
# after the code read fine.
#
#   ./deliver.sh <payload-file> [event-name] [url]
#
# GITHUB_WEBHOOK_SECRET must be set and must match the receiver's.
set -eu

payload="${1:?usage: deliver.sh <payload-file> [event] [url]}"
event="${2:-push}"
url="${3:-http://127.0.0.1:8899/hook}"
: "${GITHUB_WEBHOOK_SECRET:?set GITHUB_WEBHOOK_SECRET}"

# GitHub signs the RAW REQUEST BODY — every byte, before any parsing. Anything
# that reformats the JSON here (a jq pass, a trailing newline) changes the
# digest, which is why the file is streamed to both openssl and curl untouched.
sig=$(openssl dgst -sha256 -mac HMAC -macopt "key:$GITHUB_WEBHOOK_SECRET" -binary < "$payload" | od -An -v -tx1 | tr -d ' \n')

# A delivery id is a UUID per attempt. GitHub reuses it across REDELIVERIES of
# the same event, which is what makes it usable for deduplication.
delivery="${DELIVERY_ID:-$(uuidgen | tr 'A-Z' 'a-z')}"

curl -sS -o /dev/null -w '%{http_code}\n' \
  -X POST "$url" \
  -H 'Content-Type: application/json' \
  -H "User-Agent: GitHub-Hookshot/oath-deliver" \
  -H "X-GitHub-Event: $event" \
  -H "X-GitHub-Delivery: $delivery" \
  -H "X-Hub-Signature-256: sha256=$sig" \
  --data-binary "@$payload"
