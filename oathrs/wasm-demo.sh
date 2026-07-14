#!/usr/bin/env bash
# Cross-compile the kernel to wasm32-wasip1 and demonstrate hash + gate +
# verify of one example. The library (identity, gate, eval, gen, verify,
# analyses) is pure and builds unchanged; the `prove` module (z3 subprocess) is
# native-only and excluded via --no-default-features.
#
# If a wasm runtime (wasmtime) is present the demo actually runs; otherwise the
# green cross-compile plus the printed invocation is the deliverable.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
TARGET=wasm32-wasip1
WASM="$HERE/target/$TARGET/release/oathrs.wasm"

echo "== rustup target =="
rustup target list --installed | grep -q "$TARGET" \
  && echo "  $TARGET installed" \
  || { echo "  installing $TARGET"; rustup target add "$TARGET" || exit 1; }

echo "== build library (no SMT) =="
( cd "$HERE" && cargo build --lib --release --target "$TARGET" --no-default-features ) \
  || { echo "  LIBRARY BUILD FAILED"; exit 1; }
echo "  PASS: library cross-compiled"

echo "== build CLI binary (no SMT) =="
( cd "$HERE" && cargo build --release --target "$TARGET" --no-default-features ) \
  || { echo "  BIN BUILD FAILED"; exit 1; }
echo "  PASS: $(basename "$WASM") built ($(wc -c < "$WASM" | tr -d ' ') bytes)"

echo "== host imports (should be WASI only; no z3/threads) =="
python3 - "$WASM" <<'PY'
data=open(__import__('sys').argv[1],'rb').read()
assert data[:4]==b'\x00asm', "not a wasm module"
i=8
def leb(d,p):
    r=s=0
    while True:
        b=d[p]; r|=(b&0x7f)<<s; p+=1; s+=7
        if not b&0x80: return r,p
mods=set()
while i<len(data):
    sid=data[i]; i+=1; size,i=leb(data,i); end=i+size
    if sid==2:
        n,i=leb(data,i)
        for _ in range(n):
            ml,i=leb(data,i); m=data[i:i+ml].decode(); i+=ml
            nl,i=leb(data,i); i+=nl; k=data[i]; i+=1
            if k==0: _,i=leb(data,i)
            elif k==1: i+=1; fl=data[i]; i+=1; _,i=leb(data,i); (leb(data,i) if fl else 0)
            elif k==2: fl=data[i]; i+=1; _,i=leb(data,i);
            elif k==3: i+=2
            mods.add(m)
        i=end
    else: i=end
print("  import modules:", sorted(mods) or "(none)")
PY

RUN="wasmtime run --dir=$ROOT $WASM"
echo "== demo: hash + gate + verify of examples/list.oath =="
if command -v wasmtime >/dev/null 2>&1; then
  echo "  \$ $RUN hash $ROOT/examples/list.oath"
  $RUN hash "$ROOT/examples/list.oath"
  echo "  \$ $RUN verify $ROOT/examples/list.oath"
  $RUN verify "$ROOT/examples/list.oath"
else
  echo "  wasmtime not installed — cross-compile is green; run with:"
  echo "    $RUN hash   $ROOT/examples/list.oath"
  echo "    $RUN verify $ROOT/examples/list.oath"
  echo "  (verify on a TERMINATING example stays within the default wasm stack;"
  echo "   reaching the 100,000 depth bound — e.g. spin — needs a larger stack:"
  echo "   build with RUSTFLAGS='-C link-arg=-zstack-size=268435456', run with"
  echo "   wasmtime --max-wasm-stack=...)."
fi
echo "DONE"
