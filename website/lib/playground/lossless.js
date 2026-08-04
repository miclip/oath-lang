// The JS side of ADMIT (#133, SPEC §3) — the one boundary Go cannot defend.
//
// Every other way octets become a `Str` is checked inside the kernel, because
// the kernel can see the original bytes. This one it cannot. `syscall/js`
// converts a JS string (UTF-16) to Go's UTF-8 before any Go code runs, and an
// UNPAIRED SURROGATE has no UTF-8 encoding, so the conversion substitutes
// U+FFFD. Measured through the real playground wasm:
//
//     source "A\uD800B"   ->  hash ac72a0067c94ef7b
//     source "A\uDC00B"   ->  hash ac72a0067c94ef7b
//     source "A\uD801B"   ->  hash ac72a0067c94ef7b
//     source "A�B"   ->  hash ac72a0067c94ef7b   <- all four, `accepted`
//     source "A\u{10000}B" -> hash bc803928b696598c   <- control, distinct
//
// Four distinct sources, one definition. That is the identity collapse #133
// exists to remove, and `lex`'s utf8.ValidString guard cannot catch it: the
// substituted result IS valid UTF-8. By the time Go is running, the information
// is already gone, so the check has to happen while the UTF-16 still exists —
// which is here, and only here.
//
// U+FFFD ITSELF STAYS LEGAL. It is an ordinary character and a source that
// genuinely contains one must still compile; refusing it would be the same
// mistake in the other direction. Only unpaired surrogates are refused.

// Index of the first unpaired surrogate code unit, or -1 if the string encodes
// to UTF-8 losslessly. A JS string is losslessly encodable exactly when every
// high surrogate is immediately followed by a low one and no low surrogate
// stands alone — so this predicate IS the claim, not a proxy for it.
export function firstUnpairedSurrogate(s) {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c >= 0xd800 && c <= 0xdbff) {
      const next = i + 1 < s.length ? s.charCodeAt(i + 1) : -1;
      if (next >= 0xdc00 && next <= 0xdfff) {
        i++; // a valid pair: one scalar, encodes fine
        continue;
      }
      return i; // high surrogate with no low after it
    }
    if (c >= 0xdc00 && c <= 0xdfff) return i; // low surrogate with no high before it
  }
  return -1;
}

function refusal(fn, argIndex, at, unit) {
  return JSON.stringify({
    ok: false,
    error:
      `${fn}: argument ${argIndex} contains an unpaired surrogate (U+${unit
        .toString(16)
        .toUpperCase()
        .padStart(4, "0")}) at code unit ${at}. It has no UTF-8 encoding, so ` +
      `crossing into the kernel would silently substitute U+FFFD and make ` +
      `distinct sources content-address to one definition.`,
  });
}

// Wrap the kernel's wasm exports so the check cannot be bypassed by a new call
// site. The alternative — validating at each `oathCheck(...)` call — derives its
// universe from a grep of today's callers, which is the failure mode that let
// three of #133's five boundaries hide behind each other. Guarding the EXPORT
// derives it from the thing being crossed, so a host added tomorrow is covered
// without knowing this file exists.
//
// Refusals are returned as the kernel's own JSON error shape rather than
// thrown, because `wasm.go` already establishes that a rejected input is data
// and not a crash. Every host's existing error path then handles it unchanged —
// and one of the two hosts has no try/catch at all, where a throw would take
// down the worker.
export function guardKernelExports(g) {
  for (const fn of ["oathCheck", "oathProve"]) {
    const raw = g[fn];
    if (typeof raw !== "function") {
      throw new Error(`guardKernelExports: ${fn} is not defined — call this after go.run(instance)`);
    }
    if (raw.oathLosslessGuard) continue; // idempotent: booting twice must not double-wrap
    const guarded = (...args) => {
      for (let i = 0; i < args.length; i++) {
        // Coerce rather than skip non-strings: `js.Value.String()` will coerce
        // on the Go side regardless, so checking only `typeof === "string"`
        // would inspect a different value than the one that actually crosses.
        const s = String(args[i]);
        const at = firstUnpairedSurrogate(s);
        if (at >= 0) return refusal(fn, i, at, s.charCodeAt(at));
      }
      return raw(...args);
    };
    guarded.oathLosslessGuard = true;
    g[fn] = guarded;
  }
}
