# Custody record — the `oath/*` project key

The journal proves continuity of a PUBLIC key. It says nothing about how control
of the private half changed hands, because no registry can observe that. This
file is that record, and it is deliberately separate from the specification: it
is an observation about operations, not a normative rule.

## The key

| | |
|---|---|
| public key | `4ecd572dffebe8fc36b376fdee1cb358863a6d61fda2e37fb6c6e9c4ac1ffa6c` |
| sha256 of that hex | `934d7c3bbc02ef8ad3568ae02c2254267e338a0d7e66f09fa36fb6f76944d31b` |
| governs | `oath/*` on registry.oath-lang.org, from authority revision 1 |
| KMS resource | `projects/oath-prod-503514/locations/us-central1/keyRings/oath-authority/cryptoKeys/oath-project`, version 1 |
| algorithm | `EC_SIGN_ED25519` (PureEdDSA over raw message bytes) |

## The honest custody claim

> The namespace key was **generated and briefly held locally**, then imported
> into Cloud KMS; future signing uses a non-exportable KMS-held key.

Not "HSM-originated". Not "never exportable". Importing a key does not erase the
fact that it existed outside the HSM boundary, and a custody record that implied
otherwise would be the same class of overclaim this project spends its effort
avoiding elsewhere.

## Sequence, as performed

1. Generated locally with `oath keygen`, mode 0600, outside the repository.
2. **Reserved `oath/*` on the live registry** — irreversible, and performed
   before the custody decision had settled. This version has no transfer,
   release or expiry, so the namespace is bound to this public key permanently.
3. Converted to PKCS #8 DER (RFC 8410) and verified the DER derives the SAME
   public key before anything was imported.
4. Imported as an `EC_SIGN_ED25519` key version via a `rsa-oaep-3072-sha256-aes-256`
   import job.
5. Retrieved the KMS public key and required EXACT equality with the key holding
   `oath/*`. A near-match would mean the namespace was governed by a key KMS
   could not sign for.
6. Signed a real canonical Oath statement with BOTH signers. Ed25519 is
   deterministic, so byte-identical output is the proof that KMS signs the same
   raw bytes the kernel does — a length-prefixed or pre-hashed signer would have
   produced different bytes and passed a weaker test.
7. Verified the KMS signature through the kernel's own `ed25519.Verify` call
   rather than through the tool that produced it.
8. Destroyed the PKCS #8 conversion file and the local signature artifact.

## Pre-destruction checks (reversible), performed

| check | result |
|---|---|
| KMS dry-run: signer resource and version shown | `…/cryptoKeyVersions/1`, named in the plan |
| public key equals the `oath/*` authority | `4ecd572d…` — match |
| artifact equals the manifest entry | `fa452d59a235…` — match |
| canonical envelope bytes fixed and displayed | 8 lines, `oath-publish/2` |
| no request reached the registry | dry-run; journal unchanged |
| **signer unreachable → publication fails** | aborted with a SIGNER error, not a registry error |
| no fallback to file signing | error states no fallback is attempted |
| no partial write | journal 1241 before and after |
| plan unchanged across the permission test | byte-identical |

The denial test used an unreachable key VERSION rather than an IAM removal. That
is a real limitation and is recorded rather than glossed: the operating account
holds project-owner rights, so removing `roles/cloudkms.signer` does not deny it —
verified directly, signing still succeeded after the role was removed. The code
path exercised is the same one an IAM denial would take (gcloud fails, `run`
returns an error, no fallback, abort before submission), but the specific claim
"IAM denial is enforced" has NOT been demonstrated and should not be reported as
though it had.

## Private-key inventory, before destruction

Exactly one copy of the private half exists:

- `~/.oath/keys/oath-project.key` (mode 0600, outside the repository)

Checked and clear: no GitHub repository secret, no shell-history reference, no
file tracked in git, no leftover signing temp files, no PKCS #8 conversion, no
editor recovery file, no CI reference to a key path.

**Must survive destruction** — public key `4ecd572dffebe8fc36b376fdee1cb358863a6d61fda2e37fb6c6e9c4ac1ffa6c`,
fingerprint `934d7c3bbc02ef8ad3568ae02c2254267e338a0d7e66f09fa36fb6f76944d31b`.

## Destruction, performed

| check | result |
|---|---|
| `~/.oath/keys/oath-project.key` destroyed | overwritten and unlinked |
| any other file deriving the `oath/*` public key | none |
| alternate local signer configured (`OATH_KEY`, `~/.oath/config`, GitHub secrets, git-tracked keys) | none |
| file signing via the destroyed path | fails — file absent |
| file signing with a DIFFERENT local key | refused by the AUTHORITY GATE on identity |
| `oath/*` names live after both attempts | NONE |
| KMS public key after destruction | `4ecd572d…`, fingerprint `934d7c3b…` — match |
| post-destruction KMS plan | byte-identical to the pre-destruction plan |
| post-destruction KMS signature | produced and accepted by the kernel's own verifier |

The second and third rows carry the real weight. Failure on the destroyed path
proves only that ONE COPY is gone; the custody claim is about key material, so
the check also confirmed no other usable copy exists and no alternative local
signer is configured. The attempt with a different local key is the stronger
evidence: it was refused because the submitter is not the namespace holder, which
is an identity refusal rather than an absence.

One nuance worth recording. The refused attempt DID append a journal entry —
`blocked`, unsigned, name unmoved. That is designed behaviour: a blocked
submission stores the object and journals the attempt. "No journal write" was
therefore the wrong assertion for this test; the correct one is THE NAME DID NOT
MOVE, and no `oath/*` name is live.

## The custody claim, as it now stands

> The `oath/*` key originated locally, was imported into Cloud KMS, and the only
> inventoried local private copy was destroyed before the first standard-library
> publication. Routine signing now works through KMS only.

Kept separate, because it is a different claim and is NOT established:

> The client fails closed on signer failure, but least-privilege IAM denial has
> not yet been demonstrated, because the operating identity retains project-owner
> authority.

## Outstanding

**(historical — the local copy has since been destroyed; see above)** A local private copy still existed at `~/.oath/keys/oath-project.key`, because
the CLI has no KMS signing path — `publish` and `reserve` take `--key <file>`.
Deleting it today would leave no way to sign under `oath/*` at all.

Closing this requires a signing SEAM in the kernel: an interface with a local-file
implementation and a KMS implementation, so the signer becomes a choice rather
than an assumption. Until that exists, the custody claim above is aspirational for
routine publication and true only for KMS-mediated signing.

**No delegation exists.** The same key must hold the namespace and perform any CI
publication, so a compromised release credential would control `oath/*` itself
rather than merely the right to publish into it. The intended end state is an
offline authority key delegating to a revocable release key. This is recorded
rather than hidden because there is no protocol mechanism to fix it yet.

**Admission is open.** `deploy/entrypoint.sh` runs `oath serve` with `--tokens`
and no `--authorized-keys`, so any valid Ed25519 signature may write to the live
registry. The allowlist is implemented in the kernel and has never been enabled
in production. Every prefix other than `oath/*` is claimable first-come by anyone
who reaches the registry.
