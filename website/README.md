# Oath Language website

Marketing + docs + live-corpus playground for
[Oath Language](https://github.com/miclip/oath-lang), built with Next.js
(App Router) and deployable to Vercel with zero config.

The product is consistently branded **Oath Language** (or **Oath Lang**) rather
than bare "Oath," which collides with several unrelated products in search.

## Design

Palette, wordmark, and the six pillars follow the Oath brand sheet:
cream-on-near-black, a single sage-green accent, a serif wordmark, and a
monospace voice for anything the kernel "says." The emblem is drawn from
geometry in `components/Logo.tsx`, so it stays crisp at any size and themes
with the surface it sits on.

## The playground uses real data

`lib/outcomes.json` is copied verbatim from `fixtures/prove/outcomes.json` in
the kernel repo — the machine-generated proof ledger. Hashes are the actual
SHA-256 content identities; verdicts are the actual Z3 outcomes. To refresh it:

```sh
cp ../fixtures/prove/outcomes.json lib/outcomes.json
```

## Develop

```sh
npm install
npm run dev        # http://localhost:3000
npm run build      # production build
```

## Deploy to Vercel

```sh
npm i -g vercel
vercel              # first run links/creates the project
vercel --prod       # promote to production
```

Set the Vercel project's **Root Directory** to `website/` (this folder) if the
repo root is the kernel. No environment variables are required.

## Structure

```
app/
  page.tsx              landing page
  playground/           live corpus explorer (real hashes + verdicts)
  docs/                 overview · quickstart · guarantees · architecture
components/             Logo, Nav, Footer, Pillars, CodeBlock, explorer, …
lib/corpus.ts           typed access to the real proof data + featured details
```
