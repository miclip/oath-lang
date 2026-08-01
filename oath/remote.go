package main

// Signed registry publication (#83).
//
// Signature auth was implemented on the SERVER and unreachable from any CLIENT:
// `oath serve` accepts X-Oath-Pubkey/X-Oath-Signature, but CLI `put` wrote only
// to a local store and the push script sent a bearer token. So a user who ran
// `oath keygen` to publish got a key that worked locally and could not
// authenticate to the registry at all — leaving the shared, server-vouched token
// as the only publishing path, which inverts the design's own claim that
// signatures are the real mechanism and tokens the MCP-client shim.
//
// WHAT THE SIGNATURE BINDS, and why signing the whole body is the right choice:
// the server verifies over the ENTIRE JSON-RPC request body, so one signature
// covers the operation (`"name":"put"`), the artifact source, and every argument
// the registry will consult. A signature over the artifact bytes alone would be
// valid forever, for any name, in any operation — a bearer credential wearing a
// signature's clothing.
//
// The bytes signed are the bytes sent, verbatim. Nothing re-serializes the body
// between signing and transmission: a re-marshal that reorders a key or changes
// whitespace produces a signature failure indistinguishable from a bad key, and
// that class of bug is why this belongs in the kernel once rather than in each
// script.
//
// REPLAY IS NOW CLOSED, by a different mechanism than this comment once predicted.
// It said a fix would need "a nonce or timestamp binding plus server-side state".
// It does not: the publication envelope (SPEC §8.6) binds the PARENT hash and the
// per-name REVISION, so a captured envelope names a transition that no longer
// exists and is refused with no server-side table at all. The revision is what
// makes it survive an A→B→A cycle, where a parent hash alone becomes valid again.
//
// WHAT REMAINS OPEN is narrower and different: the envelope expresses a
// compare-and-swap, and the filesystem store does not enforce one. Compare and
// update are separate operations, so two correctly signed publications naming the
// same parent can both verify before one overwrites the other. Historical replay
// is prevented; concurrent lost updates are not. That is the transactional store's
// job, which makes the Postgres cutover a correctness dependency rather than an
// upgrade.
//
// This comment is kept rather than deleted because the prediction it got wrong is
// worth preserving: the obvious defence (nonce plus state) was more machinery and
// a weaker guarantee than binding what the author was actually claiming.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"
)

// mcpCallSigned makes one signed MCP tool call and returns the tool's text output.
// The body is signed and transmitted verbatim; nothing re-serializes it in between.
//
// Reads are signed too, because `oath serve` authenticates every request — there is
// no anonymous access to authenticate a query against.
func mcpCallSigned(endpoint string, priv ed25519.PrivateKey, pubHex, tool string, args map[string]any) (string, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", strings.TrimSuffix(endpoint, "/")+"/mcp", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Oath-Pubkey", pubHex)
	req.Header.Set("X-Oath-Signature", hex.EncodeToString(ed25519.Sign(priv, body)))
	resp, err := (&http.Client{Timeout: 300 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("registry rejected the signature (401) on %s", tool)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned HTTP %d on %s: %s", resp.StatusCode, tool, strings.TrimSpace(string(raw)))
	}
	var rpc struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return "", fmt.Errorf("registry returned unparseable JSON-RPC: %s", strings.TrimSpace(string(raw)))
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("registry: %s", rpc.Error.Message)
	}
	var b strings.Builder
	for _, c := range rpc.Result.Content {
		b.WriteString(c.Text)
	}
	if rpc.Result.IsError {
		return b.String(), fmt.Errorf("%s", strings.TrimSpace(b.String()))
	}
	return b.String(), nil
}

// clientKey is set by cmdPublish so the query helpers can sign. Held here rather
// than threaded through every helper signature; the process publishes as one
// identity per invocation.
var clientSigner Signer
var clientPub string

type headResp struct {
	Parent       string `json:"parent"`
	ParentRev    int    `json:"parent_rev"`
	EnvelopeB64  string `json:"envelope_b64"`
	AuthorPubkey string `json:"author_pubkey"`
}

func remoteHead(endpoint, name, hash string) (headResp, error) {
	var h headResp
	args := map[string]any{"name": name}
	if hash != "" {
		args["hash"] = hash
	}
	out, err := mcpCallSignedBy(context.Background(), endpoint, clientSigner, "head", args)
	if err != nil {
		return h, err
	}
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		return h, fmt.Errorf("registry head response is not JSON (is it running a kernel without `head`?): %s", strings.TrimSpace(out))
	}
	return h, nil
}

// remoteNameRevision asks the REGISTRY what a publication of name would replace.
// The registry is the authority: a local guess would sign a transition that does
// not exist there.
func remoteNameRevision(endpoint, name string) (string, int, error) {
	h, err := remoteHead(endpoint, name, "")
	if err != nil {
		return "", 0, err
	}
	if h.Parent == "" {
		return noParent, 0, nil
	}
	return h.Parent, h.ParentRev, nil
}

// remoteEnvelopeOf returns the envelope bytes the registry recorded for an
// artifact, so the client can confirm the persisted statement is byte-identical to
// what it signed.
func remoteEnvelopeOf(endpoint, hash string) (string, error) {
	h, err := remoteHead(endpoint, "", hash)
	if err != nil {
		return "", err
	}
	return h.EnvelopeB64, nil
}

// remotePutSigned publishes source with an author statement. envBytes is sent
// EXACTLY as signed.
func remotePutSigned(endpoint, source, envBytes, sig, pubHex string) (string, error) {
	return mcpCallSignedBy(context.Background(), endpoint, clientSigner, "put", map[string]any{
		"source": source, "envelope": envBytes, "signature": sig,
	})
}

// remotePut publishes source to a registry over MCP, authenticated by an
// Ed25519 signature over the request body. The key never leaves the process and
// is not sent; the pubkey travels as the claimed principal and the server
// verifies the signature against it.
func remotePut(endpoint, keyPath, source, contextHash string) (string, error) {
	priv, pubHex := loadSigningKey(keyPath)

	args := map[string]any{"source": source}
	if contextHash != "" {
		args["context"] = contextHash
	}
	// No "author" field: with a signature the principal IS the key, and a
	// client-supplied author label would be an unverified claim sitting beside a
	// verified one. The server ignores it for signed requests; omitting it keeps
	// the request honest about what is actually attested.
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "put", "arguments": args},
	})
	if err != nil {
		return "", err
	}

	// Sign the exact bytes that will be transmitted.
	sig := ed25519.Sign(priv, body)

	req, err := http.NewRequest("POST", strings.TrimSuffix(endpoint, "/")+"/mcp", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Oath-Pubkey", pubHex)
	req.Header.Set("X-Oath-Signature", hex.EncodeToString(sig))

	resp, err := (&http.Client{Timeout: 300 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("registry rejected the signature (401). The key is not the problem if `oath put` works locally: check the endpoint is the registry's /mcp and that no proxy rewrites the request body — the signature covers the body verbatim")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var rpc struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return "", fmt.Errorf("registry returned unparseable JSON-RPC: %s", strings.TrimSpace(string(raw)))
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("registry: %s", rpc.Error.Message)
	}
	var b strings.Builder
	for _, c := range rpc.Result.Content {
		b.WriteString(c.Text)
	}
	return b.String(), nil
}

// cmdRemotePut reads each source file and publishes it, signed, in order. Order
// matters on a cold registry: a definition cannot elaborate before its
// dependencies exist.
func cmdRemotePut(endpoint, keyPath string, files []string, contextHash string) {
	if keyPath == "" {
		fail(fmt.Errorf("--remote requires --key: publishing to a registry unsigned would fall back to a shared bearer token, which is the mechanism this replaces"))
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fail(err)
		}
		out, err := remotePut(endpoint, keyPath, string(src), contextHash)
		if err != nil {
			fail(fmt.Errorf("%s: %w", f, err))
		}
		fmt.Print(out)
	}
}

// remoteReserve submits a signed reservation to a registry.
func remoteReserve(ctx context.Context, endpoint string, s Signer, pubHex string, octets []byte, sig string) (string, error) {
	return mcpCallSignedBy(ctx, endpoint, s, "reserve", map[string]any{
		"envelope": encodeEnvelopeB64(octets), "signature": sig,
	})
}

// remoteAuthority reads a prefix's CURRENT authority state from a registry.
//
// A reservation is a compare-and-swap, so the state it names must come from the
// registry the claim is for. Deriving it locally would sign against a state the
// target has never been in, and the swap would refuse it — correctly, and in a
// way that reads like a bug rather than like a stale read.
func remoteAuthority(ctx context.Context, endpoint string, s Signer, namespace string) (string, *big.Int, error) {
	out, err := mcpCallSignedBy(ctx, endpoint, s, "authority", map[string]any{"name": namespace})
	if err != nil {
		return "", nil, err
	}
	var resp struct {
		Authority string `json:"authority"`
		Rev       string `json:"authority_rev"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", nil, fmt.Errorf("registry returned an unreadable authority record: %w", err)
	}
	rev, ok := new(big.Int).SetString(resp.Rev, 10)
	if !ok {
		return "", nil, fmt.Errorf("registry returned a non-decimal authority revision %q", resp.Rev)
	}
	return resp.Authority, rev, nil
}

// mcpCallSignedBy is mcpCallSigned over a Signer rather than a raw private key,
// so a remote call can be authenticated by a key this process does not hold.
func mcpCallSignedBy(ctx context.Context, endpoint string, s Signer, tool string, args map[string]any) (string, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		return "", err
	}
	pub, err := s.PublicKey(ctx)
	if err != nil {
		return "", err
	}
	// The REQUEST signature authenticates the caller; it is a different signature
	// from the one over the envelope, and both must come from the same signer or
	// the registry would authenticate one key while recording a statement by
	// another.
	sig, err := s.Sign(ctx, body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(endpoint, "/")+"/mcp", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Oath-Pubkey", hex.EncodeToString(pub))
	req.Header.Set("X-Oath-Signature", hex.EncodeToString(sig))
	resp, err := (&http.Client{Timeout: 300 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("registry rejected the signature (401) on %s: %s", tool, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned HTTP %d on %s: %s", resp.StatusCode, tool, strings.TrimSpace(string(raw)))
	}
	var rpc struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return "", fmt.Errorf("registry returned unparseable JSON-RPC: %s", strings.TrimSpace(string(raw)))
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("registry: %s", rpc.Error.Message)
	}
	var b strings.Builder
	for _, c := range rpc.Result.Content {
		b.WriteString(c.Text)
	}
	if rpc.Result.IsError {
		return b.String(), fmt.Errorf("%s", strings.TrimSpace(b.String()))
	}
	return b.String(), nil
}

// remoteDelegationRev reads a prefix's current permission-state version from a
// registry. Like the authority state, it must come from the registry the
// statement is FOR: signing against a locally-derived value would produce a
// compare-and-swap the target has never been in.
func remoteDelegationRev(ctx context.Context, endpoint string, s Signer, namespace string) *big.Int {
	out, err := mcpCallSignedBy(ctx, endpoint, s, "authority", map[string]any{"name": namespace})
	if err != nil {
		return big.NewInt(0)
	}
	var resp struct {
		DelegationRev string `json:"delegation_rev"`
	}
	if json.Unmarshal([]byte(out), &resp) != nil || resp.DelegationRev == "" {
		return big.NewInt(0)
	}
	if v, ok := new(big.Int).SetString(resp.DelegationRev, 10); ok {
		return v
	}
	return big.NewInt(0)
}

// authorityView is a prefix's governance as read from ONE store, together with
// which store that was. The provenance travels WITH the data because the whole
// point of #104 is that the same numbers mean different things depending on where
// they came from — a local reading of a prefix held on a registry is not a stale
// answer to the question asked, it is a confident answer to a different one.
type authorityView struct {
	Holder        string
	Rev           *big.Int
	DelegationRev *big.Int
	Delegates     []string
	Source        string // human-readable: the endpoint, or the local store path
	Authoritative bool   // is this the state a reservation would be evaluated against?
}

// remoteAuthorityView reads the full governance record from the registry a
// reservation would actually be submitted to.
func remoteAuthorityView(ctx context.Context, endpoint string, s Signer, namespace string) (authorityView, error) {
	out, err := mcpCallSignedBy(ctx, endpoint, s, "authority", map[string]any{"name": namespace})
	if err != nil {
		return authorityView{}, err
	}
	var resp struct {
		Authority string   `json:"authority"`
		Rev       string   `json:"authority_rev"`
		DelRev    string   `json:"delegation_rev"`
		Delegates []string `json:"delegates"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return authorityView{}, fmt.Errorf("registry returned an unreadable authority record: %w", err)
	}
	rev, ok := new(big.Int).SetString(resp.Rev, 10)
	if !ok {
		return authorityView{}, fmt.Errorf("registry returned a non-decimal authority revision %q", resp.Rev)
	}
	drev, ok := new(big.Int).SetString(resp.DelRev, 10)
	if !ok {
		// An older registry predates delegation_rev. Absent is not zero-with-
		// confidence, but the holder record is still authoritative and that is what
		// reservation advice turns on.
		drev = nil
	}
	return authorityView{Holder: resp.Authority, Rev: rev, DelegationRev: drev,
		Delegates: resp.Delegates, Source: endpoint, Authoritative: true}, nil
}
