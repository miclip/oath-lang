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

## Outstanding

**A local private copy still exists** at `~/.oath/keys/oath-project.key`, because
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
