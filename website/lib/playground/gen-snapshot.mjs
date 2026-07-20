// Serializes the committed store (codebase/) into a single JSON the browser can
// fetch and mount into MemFS, so a visitor can paste `sort` and have insert/
// length/count resolve. Run from repo root: node website/lib/playground/gen-snapshot.mjs
import fs from 'fs'; import path from 'path';
const REPO = path.resolve(process.argv[2] || '.');
const SRC = path.join(REPO, 'codebase');
const ROOT = '/store';
const out = { root: ROOT, files: {} };
const b64 = p => fs.readFileSync(p).toString('base64');
out.files[`${ROOT}/names.json`] = b64(path.join(SRC, 'names.json'));
out.files[`${ROOT}/log.jsonl`] = b64(path.join(SRC, 'log.jsonl'));
for (const d of ['objects', 'meta'])
  for (const f of fs.readdirSync(path.join(SRC, d)))
    out.files[`${ROOT}/${d}/${f}`] = b64(path.join(SRC, d, f));
const dest = path.join(REPO, 'website/public/corpus-snapshot.json');
fs.writeFileSync(dest, JSON.stringify(out));
console.error(`wrote ${dest}: ${Object.keys(out.files).length} files, ${(fs.statSync(dest).size/1024).toFixed(0)} KB`);
