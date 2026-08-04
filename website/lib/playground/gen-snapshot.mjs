// Serializes the committed store (codebase/) into a single JSON the browser can
// fetch and mount into MemFS, so a visitor can paste `sort` and have insert/
// length/count resolve. Run from repo root: node website/lib/playground/gen-snapshot.mjs
import fs from 'fs'; import path from 'path';
import { fileURLToPath } from 'url';

// Exported so `check-playground-snapshot` can ask "would regenerating change
// anything?" by CALLING THE GENERATOR rather than by re-implementing the
// comparison. A hand-written checker compares the fields its author remembered
// — here, that would have been names.json — and silently accepts drift in
// objects, meta or the journal.
export function buildSnapshot(repo) {
  const SRC = path.join(repo, 'codebase');
  const ROOT = '/store';
  const out = { root: ROOT, files: {} };
  const b64 = p => fs.readFileSync(p).toString('base64');
  out.files[`${ROOT}/names.json`] = b64(path.join(SRC, 'names.json'));
  out.files[`${ROOT}/log.jsonl`] = b64(path.join(SRC, 'log.jsonl'));
  for (const d of ['objects', 'meta'])
    for (const f of fs.readdirSync(path.join(SRC, d)).sort())
      out.files[`${ROOT}/${d}/${f}`] = b64(path.join(SRC, d, f));
  return out;
}

export const SNAPSHOT_PATH = 'website/public/pgrt/corpus-snapshot.json';

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const REPO = path.resolve(process.argv[2] || '.');
  const out = buildSnapshot(REPO);
  const dest = path.join(REPO, SNAPSHOT_PATH);
  fs.writeFileSync(dest, JSON.stringify(out));
  console.error(`wrote ${dest}: ${Object.keys(out.files).length} files, ${(fs.statSync(dest).size/1024).toFixed(0)} KB`);
}
