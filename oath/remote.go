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
