#!/bin/sh
# One image, two roles. Cloud Run's request-serving service runs `serve`
# (default CMD); the scheduled Cloud Run Job runs `worker`. Anything else is
# passed straight through to the `oath` CLI.
set -eu

mode="${1:-serve}"
case "$mode" in
serve)
	# Cloud Run injects $PORT (default 8080). Tokens come from a Secret Manager
	# volume; principals are authenticated by bearer token (client `author`
	# fields are ignored). See docs/registry-auth.md for the signature-based
	# direction that will make tokens a transport shim.
	exec oath serve \
		--http "0.0.0.0:${PORT:-8080}" \
		--tokens "${OATH_TOKENS_FILE:-/secrets/tokens/tokens.json}"
	;;
worker)
	# Drain the proof queue once and exit; Cloud Scheduler re-invokes the Job.
	# --scan also picks up any tested-but-unproven defs for background upgrade.
	# Sign verdicts when a registry key is mounted (OATH_KEY points at it);
	# otherwise proofs are recorded unsigned (still fully re-verifiable).
	if [ -n "${OATH_KEY:-}" ] && [ -f "${OATH_KEY}" ]; then
		exec oath prove-worker --once --scan --key "${OATH_KEY}"
	fi
	exec oath prove-worker --once --scan
	;;
*)
	exec oath "$@"
	;;
esac
