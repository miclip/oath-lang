package main

import "testing"

// #104: a location flag that is accepted and IGNORED is a query-integrity bug.
//
// The guard is a table plus a loop in main(), which is not directly callable, so
// this pins the TABLE — the thing that actually decides. A command gaining or
// losing a remote path must be a deliberate edit to remoteCapable, and this test
// is what makes an accidental edit visible.
func TestRemoteCapableTableIsAccurate(t *testing.T) {
	// Commands that DO reach a registry. If one is dropped here, `--remote` on it
	// starts being refused and a working workflow breaks loudly — which is the
	// right failure, but it must be deliberate.
	for _, c := range []string{"put", "publish", "license"} {
		if !remoteCapable[c] {
			t.Errorf("%q reaches a registry but is absent from remoteCapable, so --remote on it is now refused", c)
		}
	}
	// Commands that read the LOCAL store only. Listing one of these would restore
	// the exact defect: --remote silently accepted, answer taken from elsewhere.
	for _, c := range []string{"ls", "log", "get", "explain", "find", "audit", "verify", "prove"} {
		if remoteCapable[c] {
			t.Errorf("%q has no remote path but is listed as remote-capable: --remote would be "+
				"accepted and silently ignored, and the answer would come from the local store", c)
		}
	}
	if remoteCapableList() == "" {
		t.Error("remoteCapableList() is empty; the error message would name no alternative")
	}
}
