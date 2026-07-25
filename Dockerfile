# syntax=docker/dockerfile:1
#
# The Oath registry image: the `oath` kernel plus a pinned Z3. One image runs
# both roles — `serve` (the HTTP MCP endpoint) and `worker` (the proof pool) —
# selected by the entrypoint's first argument. The kernel has zero Go
# dependencies (no go.sum to fetch) and shells out to `z3` as a subprocess, so
# the binary is a static CGO-free build and the runtime only needs the solver on
# PATH.

# ---- build the kernel ----
FROM golang:1.25-bookworm AS build
WORKDIR /src/oath
COPY oath/ ./
# Build with -tags cloud so the registry image carries the GCS+Postgres backend
# (docs/store-drivers.md). It stays DORMANT unless OATH_BACKEND=cloud, so this is
# backward-compatible with the filesystem store; activation is an env flip, no
# image rebuild. The kernel/CI default build stays zero-dependency — only this
# deployment image pulls the pg driver.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -tags cloud -o /out/oath .

# ---- runtime: kernel + pinned z3 ----
# Proof outcomes are solver-version-sensitive (SPEC §10.5); pin the SAME z3
# 4.16.0 the conformance CI pins, so a deployed worker's verdicts match the
# canonical fixtures. The base MUST match that z3 build's glibc: the pinned
# z3-…-glibc-2.39 binary needs glibc ≥ 2.39, so the runtime is ubuntu:24.04
# (glibc 2.39, same as the CI runner) — on debian bookworm (glibc 2.36) z3
# fails to execute and every proof aborts environmentally.
FROM ubuntu:24.04 AS runtime
ARG Z3_VERSION=4.16.0
ARG Z3_DIST=z3-4.16.0-x64-glibc-2.39
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip \
 && curl -sSL -o /tmp/z3.zip "https://github.com/Z3Prover/z3/releases/download/z3-${Z3_VERSION}/${Z3_DIST}.zip" \
 && unzip -q /tmp/z3.zip -d /opt \
 && ln -s "/opt/${Z3_DIST}/bin/z3" /usr/local/bin/z3 \
 && apt-get purge -y curl unzip && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/* /tmp/z3.zip
COPY --from=build /out/oath /usr/local/bin/oath
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
# The store lives on the gcsfuse-mounted volume in production; this default keeps
# a local `docker run` self-contained.
ENV OATH_STORE=/store
RUN mkdir -p /store
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["serve"]
