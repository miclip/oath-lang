package main

import (
	"os"
	"strings"
	"testing"
)

// The worker's transport boundary, pinned structurally because the thing being
// prevented is a future deployment "simplification" rather than a bug in today's
// code — and a comment is exactly what such a change walks past.
//
// The worker binds names (deferred require_proven repoints), so it cannot be
// dismissed as evidence-only. What makes that safe is that admission already
// happened at put time. Routing its writes through the hosted publication API
// would either fail — its entries are unsigned — or invite someone to relax the
// freeze to make the worker work again, which is the failure this guards.
func TestWorkerIsNotAnAdmissionPath(t *testing.T) {
	src, err := os.ReadFile("prove_worker.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	// Calls that would put the worker on the hosted publication path.
	for _, forbidden := range []string{
		"remotePut", "remotePutSigned", "remoteText", "mcpCallSigned",
		"mcpCallTool", "apiPutSigned", "handleRPC",
	} {
		if strings.Contains(body, forbidden+"(") {
			t.Errorf("prove_worker.go calls %s: the worker must not write through the hosted "+
				"publication API. Its entries are unsigned, so the name-creation freeze would "+
				"surface as an outage — and the tempting fix is to weaken the freeze. Give it a "+
				"dedicated signing principal, or an evidence-ingestion endpoint that cannot bind "+
				"names.", forbidden)
		}
	}

	// The invariant must remain WRITTEN DOWN next to the code it constrains: a
	// future reader needs the reasoning, not just a failing test.
	if !strings.Contains(body, "TRANSPORT INVARIANT") {
		t.Error("the transport invariant comment was removed from prove_worker.go; the structural " +
			"check survives but the reasoning a maintainer needs does not")
	}
}

// A gate job may only exist for a name whose admission already ran. Enqueueing is
// reachable only from the put path, and this pins that: if some other caller gains
// the ability to enqueue, the queue becomes an admission channel that never ran an
// admission check.
func TestOnlyThePutPathEnqueuesGateJobs(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"proofq.go": true, "api.go": true, "prove_worker.go": true}
	for _, f := range files {
		n := f.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") || allowed[n] {
			continue
		}
		b, rerr := os.ReadFile(n)
		if rerr != nil {
			continue
		}
		if strings.Contains(string(b), "EnqueueProof(") {
			t.Errorf("%s enqueues a proof job. Gate jobs must originate only in the put path, "+
				"which is where admission — including the unowned-name freeze — is decided. "+
				"Enqueueing elsewhere makes the queue a way to bind a name without ever having "+
				"been admitted.", n)
		}
	}
}
