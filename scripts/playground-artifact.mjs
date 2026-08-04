// The two halves of #148 that can be written and tested WITHOUT the bucket:
// producing a commit manifest, and verifying a downloaded artifact against one.
// CI does the uploading between them; that part is untestable until the bucket
// exists, and is deliberately not stubbed here — a stub would look like coverage.
//
//   node scripts/playground-artifact.mjs manifest [--commit <sha>] [--base-url <url>]
//   node scripts/playground-artifact.mjs verify <manifest.json> <artifact>
//
// The layout is content-addressed on purpose (#148):
//
//   wasm/sha256/<artifact-digest>/oath.wasm   what the bytes ARE      (immutable)
//   wasm/commits/<git-commit>/manifest.json   which bytes belong to   (immutable)
//                                             this source
//
// Two levels because they answer different questions. Collapsing them into one
// mutable "latest" pointer would reintroduce exactly the movable identity this
// design exists to remove.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const ARTIFACT = path.join(ROOT, "website/public/pgrt/oath.wasm");
const DEFAULT_BASE = "https://storage.googleapis.com/oath-playground-artifacts";

const sha256 = (file) => crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");

function manifest(argv) {
  const at = (flag, dflt) => {
    const i = argv.indexOf(flag);
    return i >= 0 && argv[i + 1] ? argv[i + 1] : dflt;
  };
  // The commit is an INPUT, not something read from the working tree by default
  // in CI: the artifact belongs to the source that produced it, and a job that
  // re-derives HEAD could attribute bytes to a commit that moved underneath it.
  const commit = at("--commit", execFileSync("git", ["rev-parse", "HEAD"], { cwd: ROOT, encoding: "utf8" }).trim());
  const base = at("--base-url", DEFAULT_BASE).replace(/\/+$/, "");
  if (!fs.existsSync(ARTIFACT)) {
    console.error(`ERROR: ${path.relative(ROOT, ARTIFACT)} is missing — run 'make playground-assets'`);
    process.exit(1);
  }
  const digest = sha256(ARTIFACT);
  process.stdout.write(JSON.stringify({
    source_commit: commit,
    artifact_sha256: digest,
    artifact_url: `${base}/wasm/sha256/${digest}/oath.wasm`,
    // Recorded because they are what makes the digest STABLE. A future build
    // that drops either flag produces different bytes for identical source, and
    // a reader comparing digests across commits needs to know that changed.
    build: { buildvcs: false, trimpath: true },
  }, null, 2) + "\n");
}

function verify(argv) {
  const [manifestPath, artifactPath] = argv;
  if (!manifestPath || !artifactPath) {
    console.error("usage: playground-artifact.mjs verify <manifest.json> <artifact>");
    process.exit(2);
  }
  let m;
  try {
    m = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  } catch (e) {
    console.error(`ERROR: manifest unreadable: ${e.message}`);
    process.exit(1);
  }
  // FAIL CLOSED on a malformed manifest. A missing field must not read as "no
  // constraint to check" — that is how a verification step becomes a no-op that
  // still exits 0, which is worse than not having one.
  if (typeof m.artifact_sha256 !== "string" || !/^[0-9a-f]{64}$/.test(m.artifact_sha256)) {
    console.error(`ERROR: manifest has no valid artifact_sha256 — refusing to treat an unverifiable artifact as verified`);
    process.exit(1);
  }
  if (!fs.existsSync(artifactPath)) {
    console.error(`ERROR: artifact ${artifactPath} does not exist`);
    process.exit(1);
  }
  const got = sha256(artifactPath);
  if (got !== m.artifact_sha256) {
    console.error("ERROR: artifact digest MISMATCH — refusing to use it");
    console.error(`  manifest says: ${m.artifact_sha256}`);
    console.error(`  file is:       ${got}`);
    console.error(`  source_commit: ${m.source_commit ?? "(absent)"}`);
    process.exit(1);
  }
  console.log(`artifact verified ✓ sha256 ${got.slice(0, 16)}… for commit ${(m.source_commit ?? "?").slice(0, 12)}`);
}

const [, , cmd, ...rest] = process.argv;
if (cmd === "manifest") manifest(rest);
else if (cmd === "verify") verify(rest);
else {
  console.error("usage: playground-artifact.mjs manifest|verify …");
  process.exit(2);
}
