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
// NOT SOLVED HERE, deliberately: replay. An identical signed put is idempotent
// for the OBJECT (content addressing) but it also REPOINTS the name, so a
// captured envelope could later roll a name back to an earlier version. Fixing
// that needs a nonce or timestamp binding plus server-side state, which is a
// design decision about the store backend rather than a client change. Tracked
// on #83 rather than half-built here.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
var clientPriv ed25519.PrivateKey
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
	out, err := mcpCallSigned(endpoint, clientPriv, clientPub, "head", args)
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
		return noParent, firstRev, nil
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
	return mcpCallSigned(endpoint, clientPriv, clientPub, "put", map[string]any{
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
