package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// THE EMITTED RUNTIME DECLARES NO STORAGE THAT COULD HOLD A REQUEST POINTER (#165).
//
// ONE DECLARATION IS PERMITTED, AND IT IS PERMITTED ON A TIMING ARGUMENT, NOT ON
// A CLAIM THAT IT HOLDS NOTHING. `static OBlock *o_arena_blocks` is the arena's
// ownership root, and it DOES retain request memory: every OVal and every buffer
// a request allocates sits in a block reachable from it through `base`. The
// invariant that replaces "no static storage at all" is about WHEN:
//
//	the root may retain request allocations FOR THE DURATION of the request;
//	`o_arena_release` clears it before freeing, so nothing is reachable from
//	static storage after the release point;
//	every OTHER static pointer stays forbidden, because nothing clears one.
//
// So this test is half the argument and cannot be read as the whole of it. It
// establishes that the root is the ONLY such slot; that the root is CLEARED is
// what makes permitting it different from permitting a retention slot, and
// TestArenaReleaseClearsItsOwnershipRoot is where that half lives. An exemption
// for a root nothing clears would be an exemption for exactly the retention
// forbidden here.
//
// The exemption is the exact declaration and nothing else — same NAME at a
// different TYPE fails, and the same TYPE under a different name fails — because
// the argument was made about one root whose clearing is witnessed, not about a
// shape a scanner could recognise. Both are controlled below.
//
// The memory design for this backend is arena-shaped: everything a request
// allocates is released once its answer has been serialised. That is sound only
// while nothing survives the call that produced it — and the load-bearing case
// is a CAPABILITY, because a capability is the only thing an Oath program hands
// to the host. `o_cap_emit` opens, writes and closes inside the call today; a
// later batching sink that buffered its argument would break the arena silently,
// with no test failing and no comment contradicted.
//
// WHAT THIS ESTABLISHES, AND WHAT IT DOES NOT. It checks DECLARATIONS: no object
// of static storage duration exists that could hold a pointer. It does NOT
// establish that no capability retains request memory, and the difference is not
// pedantic — a provider could call `putenv(o_cstr(arg))`, which keeps the buffer
// it is handed, or pass `arg` to a worker thread, declaring nothing. Neither is
// visible here.
//
// So this closes the storage route and names the other one rather than implying
// it is covered. Retention through a LIBC CALL or a thread handoff needs an
// audit at the capability boundary, and `docs/experiments/issue-165-memory.md`
// records it as open.
//
// THE UNIVERSE IS STATIC STORAGE DURATION, NOT COLUMN ZERO. A first version of
// this test scanned file-scope lines only, so `static OVal *stash;` written
// INSIDE `cap_emit_code` — which is how a batching sink would actually be
// written, and which has static storage duration all the same — passed it. The
// claim is about what outlives a call, so the scan is about `static`, at any
// indentation.
//
// TWO MORE DECLARATIONS ARE PERMITTED, ON A DIFFERENT ARGUMENT, AND THE
// POINTER RULE IS UNCHANGED. SPEC §14.2 requires a runtime refusal inside a
// handler to become a 500 rather than an exit — a remote party must never be
// able to end the process — so the refusal has to unwind from arbitrary depth
// to the serve loop. In C that is `setjmp`/`longjmp`, the jump target must live
// in a frame that stays live across the handler call, and the door that raises
// it takes no context, so the target and the in-flight flag are reachable only
// through static storage. There is no arrangement of this runtime that avoids
// them; asserting the obligation harder would not create the structure it
// needs.
//
// What keeps this from being a hole in the argument above:
//
//	NEITHER IS A POINTER, so neither can be the slot a capability parks a
//	request value in — which is the retention this test exists to forbid, and
//	the same reasoning the scanner already applies to non-pointer `const`
//	storage. The exemptions are exact declarations, and a `*` appearing in
//	either is a failure below.
//	THE JUMP TARGET IS RE-ESTABLISHED PER REQUEST, at the top of the loop,
//	after the previous request's arena has been released and before anything is
//	allocated for the next one — so no live arena pointer is in scope at the
//	moment its register image is saved. That half is structural and is
//	witnessed by TestRefusalBoundaryIsArmedPerRequest, exactly as the root's
//	clearing is witnessed rather than asserted.
//
// AND TWO MORE SINCE #173, ON A THIRD ARGUMENT — the one place where the claim
// this file counts genuinely CHANGED rather than being extended. `static OBlock
// *o_perm_blocks` is a second pointer root and nothing ever clears it, so the
// arena's temporal argument is not available to it and would be false if
// claimed. What is true instead is that it never holds a request allocation at
// all: it is carved from only while `o_perm_state` is 1, a phase entered once
// before any listener exists and left, one-way, before the serve loop runs.
//
// So the exemption is for a pointer that RETAINS FOREVER and can never retain a
// REQUEST — which is why the phase slot is permitted alongside it rather than
// waved through as an int, and why both halves are witnessed behaviourally by
// TestProcessLifetimeRegionOutlivesTheArena instead of asserted here.
//
// The counted claim is therefore a closed list of three arguments: one static
// pointer holding request memory, still the arena root, still cleared at
// release; exactly two non-pointer continuation slots, each named and typed; and
// exactly two process-lifetime slots that no request allocation can reach.
func TestEmittedRuntimeDeclaresNoStaticStorageForValues(t *testing.T) {
	rt := llvmRuntimeC
	if len(rt) < 2000 {
		t.Fatalf("the runtime source is %d bytes, so this test is not reading what it "+
			"believes it is", len(rt))
	}

	// CONTROLS, written in the forms the defect would ARRIVE in rather than the
	// form the scanner finds easiest. Both defeated an earlier version.
	for _, probe := range []struct{ name, line string }{
		{"file-scope with a trailing comment", "static OVal *o_stash;   /* a batching sink */"},
		{"function-local static", "  static OVal *stash = 0;"},
		// WITHOUT THE KEYWORD. A file-scope object has static storage duration
		// whether or not it is spelled `static`, and this scanner's universe has
		// always said so — but its regex required the keyword, so this arrived
		// as an unparseable line rather than a named one. It must be reported
		// BY NAME, which is what makes it exemptible with an argument instead of
		// merely failing.
		{"file-scope without the keyword", "OVal *o_stash_plain;"},
	} {
		got := staticVars(rt + "\n" + probe.line + "\n")
		if len(got) == 0 || got[len(got)-1].name == "" {
			t.Fatalf("the scanner does not see an injected %s, so its silence on the real "+
				"runtime is not evidence", probe.name)
		}
	}

	// THE EXEMPTION'S OWN CONTROLS. An allowlist is a hole, so both halves of the
	// key are probed: a VALUE slot wearing the permitted name, and the permitted
	// TYPE wearing another name. Either passing would mean the exemption admits a
	// class rather than a declaration — the first is how the hole would actually
	// be walked through, since renaming a slot is cheaper than arguing for it.
	for _, probe := range []struct{ name, line string }{
		{"a value slot under the permitted name", "static OVal *o_arena_blocks;"},
		{"the permitted type under another name", "static OBlock *o_stash_blocks;"},
	} {
		got := staticVars(probe.line + "\n")
		if len(got) != 1 {
			t.Fatalf("the scanner reads %d declarations from the probe %q, so this control "+
				"is not testing what it believes it is", len(got), probe.line)
		}
		if arenaOwnershipRoot(got[0]) {
			t.Fatalf("the exemption admits %s, so it is permitting a shape rather than "+
				"the one declaration argued for", probe.name)
		}
	}

	// THE CONTINUATION EXEMPTION'S OWN CONTROLS, on the same two halves. A
	// POINTER wearing a permitted name is the way this hole would actually be
	// walked through — the whole argument for these two is that they cannot hold
	// one — and a permitted type under another name would make the exemption a
	// shape rather than a declaration.
	for _, probe := range []struct{ name, line string }{
		{"a pointer under the permitted flag name", "static OVal *o_request_live;"},
		{"a pointer under the permitted target name", "static OVal *o_request_jump;"},
		{"the permitted flag type under another name", "static int o_stash_live;"},
		{"the permitted target type under another name", "static jmp_buf o_stash_jump;"},
	} {
		got := staticVars(probe.line + "\n")
		if len(got) != 1 {
			t.Fatalf("the scanner reads %d declarations from the probe %q, so this control "+
				"is not testing what it believes it is", len(got), probe.line)
		}
		if requestContinuationSlot(got[0]) {
			t.Fatalf("the continuation exemption admits %s, so it is permitting a shape "+
				"rather than the two declarations argued for", probe.name)
		}
	}

	// THE PROCESS-LIFETIME EXEMPTION'S OWN CONTROLS, on the same two halves. The
	// pointer here is exempt on an argument the arena root cannot make — nothing
	// clears it — so the probes are the two ways that argument could be
	// borrowed: a DIFFERENT pointer wearing the permitted name, and the permitted
	// types under names the region's phase does not govern.
	for _, probe := range []struct{ name, line string }{
		{"a value slot under the permitted region name", "static OVal *o_perm_blocks;"},
		{"the region's block-list type under another name", "static OBlock *o_stash_blocks;"},
		{"a pointer under the permitted phase name", "static OVal *o_perm_state;"},
		{"the phase type under another name", "static int o_stash_state;"},
	} {
		got := staticVars(probe.line + "\n")
		if len(got) != 1 {
			t.Fatalf("the scanner reads %d declarations from the probe %q, so this control "+
				"is not testing what it believes it is", len(got), probe.line)
		}
		if processLifetimeSlot(got[0]) {
			t.Fatalf("the process-lifetime exemption admits %s, so it is permitting a shape "+
				"rather than the two declarations argued for", probe.name)
		}
	}

	// THE STACK GUARD EXEMPTION'S OWN CONTROLS, on the same two halves. The
	// argument for these three is that their types cannot carry a pointer, so
	// the probes are: a POINTER wearing each permitted name — which is exactly
	// how the hole would be walked through, since renaming is cheaper than
	// arguing — and a permitted type under another name.
	for _, probe := range []struct{ name, line string }{
		{"a pointer under the permitted floor name", "OVal *o_stack_floor;"},
		{"a pointer under the permitted budget name", "static OVal *o_stack_budget;"},
		{"a pointer under the permitted rlimit-flag name", "static OVal *o_stack_from_rlimit;"},
		{"the floor's type under another name", "uintptr_t o_stash_floor;"},
		{"the budget's type under another name", "static size_t o_stash_budget;"},
		{"the flag's type under another name", "static int o_stash_from_rlimit;"},
	} {
		got := staticVars(probe.line + "\n")
		if len(got) != 1 {
			t.Fatalf("the scanner reads %d declarations from the probe %q, so this control "+
				"is not testing what it believes it is", len(got), probe.line)
		}
		if stackGuardSlot(got[0]) {
			t.Fatalf("the stack-guard exemption admits %s, so it is permitting a shape "+
				"rather than the three declarations argued for", probe.name)
		}
	}

	// AND THE PERMITTED DECLARATIONS MUST BE PRESENT. Without this an exemption
	// could outlive what it was argued for — the allowlist entry would sit here
	// matching nothing, and the next declaration that happened to be spelled that
	// way would inherit an argument nobody re-made.
	permitted, continuation, perm, guard := 0, 0, 0, 0
	for _, v := range staticVars(rt) {
		if arenaOwnershipRoot(v) {
			permitted++
		}
		if requestContinuationSlot(v) {
			continuation++
		}
		if processLifetimeSlot(v) {
			perm++
		}
		if stackGuardSlot(v) {
			guard++
		}
	}
	if guard != 3 {
		t.Errorf("the runtime declares %d of the 3 permitted stack-guard slots. The floor "+
			"is what the emitted IR loads at every function entry and the other two are "+
			"what the diagnostic reports, so none is admissible without the others; and an "+
			"exemption matching nothing is a hole waiting for a coincidence", guard)
	}
	if perm != 2 {
		t.Errorf("the runtime declares %d of the 2 permitted process-lifetime slots. The "+
			"pointer's exemption rests on the phase slot beside it — a region carved from "+
			"only while o_perm_state is 1 — so neither is admissible without the other, and "+
			"an exemption matching nothing is a hole waiting for a coincidence", perm)
	}
	if permitted != 1 {
		t.Errorf("the runtime declares the permitted arena ownership root %d times, want exactly 1. "+
			"An exemption matching nothing is a hole waiting for a coincidence; matching twice "+
			"means the block list has been duplicated and one of them is unaccounted for", permitted)
	}
	if continuation != 2 {
		t.Errorf("the runtime declares %d of the 2 permitted refusal-continuation slots. "+
			"Fewer means an exemption matches nothing and is waiting for a coincidence; "+
			"more means the boundary has been duplicated and one copy is unaccounted for", continuation)
	}

	for _, v := range staticVars(rt) {
		if arenaOwnershipRoot(v) || requestContinuationSlot(v) ||
			processLifetimeSlot(v) || stackGuardSlot(v) {
			continue
		}
		if v.name == "" {
			t.Errorf("a `static` declaration this scanner cannot parse: %s\n"+
				"An UNRECOGNISED form is a FAILURE, not a pass. The regex reads a "+
				"single-word type, so `static unsigned char *stash;` or `static struct "+
				"Holder stash;` would otherwise vanish — and vanishing is indistinguishable "+
				"from absent, which is the whole defect this test exists to prevent.", v.decl)
			continue
		}
		t.Errorf("the emitted runtime declares static storage %q (%s). A capability could "+
			"retain a request's memory there, which the arena design forbids. If this is "+
			"deliberate it needs a lifetime argument, not just a declaration", v.name, v.decl)
	}
}

// THE ROOT IS CLEARED AT RELEASE, WHICH IS WHAT MAKES ITS EXEMPTION DIFFERENT
// FROM A RETENTION SLOT (#165).
//
// `o_arena_blocks` transitively holds every allocation a request makes, so the
// scan above permits a slot that DOES retain request memory. The whole argument
// for permitting it is temporal — the retention ends at the release point — and
// nothing above measures that. This does.
//
// TWO HALVES, BECAUSE ONE OF THEM HAS NO OBSERVABLE CONSEQUENCE TODAY. The
// POST-CONDITION — the root is null afterwards, a second release is a no-op, and
// the arena is usable again — is behavioural, and the driver ASSERTS THE ROOT
// DIRECTLY rather than inferring it from a symptom. The ORDER (cleared BEFORE
// the frees, so the root never points at freed memory) changes nothing any
// current caller can see, since nothing runs between; it is pinned STRUCTURALLY,
// and saying which half rests on which evidence is the honest form.
//
// WHAT THIS DOES NOT SEE, stated because the boundary is easy to overread: the
// driver checks the root and the arena's reusability, not the memory safety of
// the traversal. A release that cleared correctly and then walked the list
// wrongly — freeing a block before reading its `next` — would satisfy every
// assertion here, since the reads usually still return the right bytes. That
// class needs a sanitizer, and no gate in this repo runs one.
//
// The driver #includes the runtime source rather than linking it, because the
// root is `static`: the property is about a name with internal linkage, and a
// separate translation unit cannot see it at all.
func TestArenaReleaseClearsItsOwnershipRoot(t *testing.T) {
	requireClang(t)

	// THE STRUCTURAL HALF. The clear must precede every `free` in the body, so
	// the function is sliced out and the two positions compared. A scanner, and
	// narrow on purpose: it reads the ONE function it names and fails if it
	// cannot find it, rather than searching the file for a reassuring substring.
	const sig = "void o_arena_release(void) {"
	i := strings.Index(llvmRuntimeC, sig)
	if i < 0 {
		t.Fatalf("the runtime does not define %q, so this test is reading nothing", sig)
	}
	body := llvmRuntimeC[i:]
	if j := strings.Index(body, "\n}"); j > 0 {
		body = body[:j]
	}
	clear, firstFree := strings.Index(body, "o_arena_blocks = 0;"), strings.Index(body, "free(")
	if clear < 0 {
		t.Error("o_arena_release does not clear o_arena_blocks, so the arena's ownership " +
			"root goes on pointing at the request after release — which is precisely the " +
			"retention every other static slot is forbidden for")
	}
	if firstFree < 0 {
		t.Error("o_arena_release frees nothing, so there is no release and the exemption " +
			"above is for a slot that simply retains")
	}
	if clear >= 0 && firstFree >= 0 && clear > firstFree {
		t.Error("o_arena_release frees before clearing its root, so the root points at freed " +
			"memory for the length of the traversal. Nothing observes that window today, " +
			"which is why it is pinned here rather than by a sanitizer")
	}

	// THE BEHAVIOURAL HALF. Allocate, release, and read the root from INSIDE the
	// translation unit; then release again and reallocate. The root is asserted
	// directly, so a release that failed to clear it is caught by the assertion
	// that names it rather than by a symptom somewhere downstream.
	driver := `#include "rt.c"
int main(void) {
  OVal *v = o_strn("hello", 5);
  if (!o_arena_blocks) { fputs("allocating left no block\n", stderr); return 1; }
  if (!v || v->slen != 5) { fputs("allocation is wrong\n", stderr); return 2; }
  o_arena_release();
  if (o_arena_blocks) { fputs("the root still points at the released request\n", stderr); return 3; }
  o_arena_release();
  if (o_arena_blocks) { fputs("a second release set the root\n", stderr); return 4; }
  OVal *w = o_strn("again", 5);
  if (!o_arena_blocks || !w || w->slen != 5) { fputs("the arena is not reusable\n", stderr); return 5; }
  o_arena_release();
  fputs("ok\n", stdout);
  return 0;
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rt.c"), []byte(llvmRuntimeC), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.c"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	// ONE BUILD, PLAIN CLANG. The driver's own assertions need nothing but a C
	// compiler, so this depends on no tooling beyond what the rest of this
	// package already requires.
	bin := filepath.Join(dir, "driver")
	cc := exec.Command("clang", "-g", "-O1", "-o", bin, "driver.c")
	cc.Dir = dir
	if b, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("the driver did not compile, so nothing below ran: %v\n%s", err, b)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("the release driver failed: %v\n%s", err, out)
	}
	// THE SETUP CONTROL. A driver that printed nothing would pass every check
	// above by never reaching one, which is this repo's most familiar way for a
	// failure path to look green.
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("the driver did not reach its own verdict, so its silence is not evidence:\n%s", out)
	}
}

// THE PROCESS-LIFETIME REGION IS NOT WHAT THE RELEASE FREES, AND NO REQUEST CAN
// REACH IT (#173).
//
// This is the other half of the static-storage argument above: `o_perm_blocks`
// is a pointer root nothing clears, permitted only because a request allocation
// can never land in it. Both directions are measured here, because either one
// alone is satisfied by a runtime with the defect the region was added to
// remove — a region that is never freed but that requests also allocate into is
// a leak, and a region requests cannot reach but the release frees is the
// dangling capability the backend used to refuse the shape over.
//
// THE PHASE IS THE WHOLE ARGUMENT, so the driver reads it rather than inferring
// it: it checks WHICH ROOT each allocation grew, on both sides of the seal.
// Asserting only that the answer survived would pass for a runtime that put
// everything in one never-released region.
//
// AND THE TRANSITION IS ONE-WAY, witnessed by a second process rather than by
// reading the guard: re-opening a sealed region is o_bug, which exits, so it
// cannot be checked in the same run as anything after it.
func TestProcessLifetimeRegionOutlivesTheArena(t *testing.T) {
	requireClang(t)

	// THE STRUCTURAL HALF: the serve loop refuses to run before the region is
	// sealed. Behaviourally unreachable from a driver — o_serve_loop binds a
	// socket and never returns — so it is pinned by reading the one function it
	// names, which fails if that function is gone.
	const sig = "static int o_serve_loop(OVal *handler,"
	if i := strings.Index(llvmRuntimeC, sig); i < 0 {
		t.Errorf("the runtime does not define %q, so this test is reading nothing", sig)
	} else {
		body := llvmRuntimeC[i:]
		if j := strings.Index(body, "\n}"); j > 0 {
			body = body[:j]
		}
		if !strings.Contains(body, "o_perm_state != 2") {
			t.Error("the serve loop does not check that the process-lifetime region is sealed " +
				"before it starts serving. Without it, a request could allocate into the " +
				"region — which is the one thing the exemption for that root rests on")
		}
	}

	driver := `#include "rt.c"

int main(int argc, char **argv) {
  /* THE ONE-WAY TRANSITION, in its own process because o_bug exits. */
  if (argc > 1 && strcmp(argv[1], "reopen") == 0) {
    o_perm_open();
    o_perm_seal();
    o_perm_open();
    fputs("a sealed region was re-opened\n", stderr);
    return 9;
  }

  /* PROVISIONING. The record and the handler closure are built here. */
  o_perm_open();
  OVal *cap = o_strn("capability", 10);
  if (!cap) { fputs("provisioning allocated nothing\n", stderr); return 1; }
  if (!o_perm_blocks) { fputs("provisioning did not grow the process-lifetime region\n", stderr); return 2; }
  if (o_arena_blocks) { fputs("provisioning went to the request arena\n", stderr); return 3; }
  OBlock *held = o_perm_blocks;
  o_perm_seal();

  /* A REQUEST. Everything it allocates must be arena memory. */
  OVal *req = o_strn("request", 7);
  if (!req || !o_arena_blocks) { fputs("a request did not grow the arena\n", stderr); return 4; }
  if (o_perm_blocks != held) { fputs("a request grew the process-lifetime region\n", stderr); return 5; }
  o_arena_release();
  if (o_arena_blocks) { fputs("the release left the arena root set\n", stderr); return 6; }
  if (o_perm_blocks != held) { fputs("the release freed or moved the process-lifetime region\n", stderr); return 7; }

  /* AND THE BYTES SURVIVE REUSE. Two hundred request-sized cycles, each writing
     over whatever the allocator hands back: a region that had been freed above
     would almost certainly be handed out again and overwritten here. Weaker
     evidence than the root checks and deliberately kept — reading freed memory
     usually succeeds, so this is what catches a release that frees the blocks
     without disturbing the root. */
  for (int i = 0; i < 200; i++) {
    char *scratch = (char *)xalloc(O_ARENA_BLOCK);
    memset(scratch, 0xA5, O_ARENA_BLOCK);
    o_arena_release();
  }
  if (cap->tag != T_STR || cap->slen != 10) {
    fputs("the process-lifetime value did not survive the arena being reused\n", stderr);
    return 8;
  }
  fputs("ok\n", stdout);
  return 0;
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rt.c"), []byte(llvmRuntimeC), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.c"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "driver")
	cc := exec.Command("clang", "-g", "-O1", "-o", bin, "driver.c")
	cc.Dir = dir
	if b, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("the driver did not compile, so nothing below ran: %v\n%s", err, b)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("the region driver failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("the driver did not reach its own verdict, so its silence is not evidence:\n%s", out)
	}

	// The re-open must be refused as a compiler bug — 70, the status every other
	// unrecoverable condition in this runtime uses — and not by returning.
	reopen, err := exec.Command(bin, "reopen").CombinedOutput()
	if err == nil {
		t.Fatalf("re-opening a sealed region returned normally:\n%s", reopen)
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("running the re-open probe: %v", err)
	}
	if ee.ExitCode() != 70 {
		t.Errorf("re-opening a sealed region exited %d, want 70:\n%s", ee.ExitCode(), reopen)
	}
	if !strings.Contains(string(reopen), "process-lifetime region") {
		t.Errorf("the re-open failure does not name what went wrong:\n%s", reopen)
	}
}

// A BLOCK-FILLING REQUEST MUST NOT DISPLACE THE ACTIVE BLOCK (#165).
//
// Only the head block is ever carved from, so whichever block is at the head is
// the arena's whole supply. A request that fills a block gets a dedicated one
// that is full the moment it is handed out — and putting THAT at the head hides
// the active block's remaining space, so the next small allocation starts a
// fresh 64K block. A request alternating a large buffer with a small value (a
// bignum magnitude, then the OVal wrapping it) then abandons a nearly empty
// block per result.
//
// BOTH SIDES OF THE BOUNDARY, because they reach the allocator by different
// routes and only one of them looks oversized. A request of EXACTLY one block
// consumes a standard block completely, so it is a dedicated allocation in every
// respect except size — a `>` test sends it down the ordinary path and puts a
// full block at the head, which is the same defect wearing the ordinary case's
// clothes. The strictly-larger case is the one anybody would think to write.
//
// Waste is invisible to every other test here: the answers are identical, the
// root is still cleared, and the release still frees everything. What changes is
// only how much was held at once, which is why this asserts the LIST SHAPE
// rather than a byte count.
func TestArenaKeepsTheActiveBlockWhenAnOversizedRequestArrives(t *testing.T) {
	requireClang(t)

	driver := `#include "rt.c"

/* 0 on success; prints which case failed and returns a distinct code otherwise. */
static int probe(size_t big_bytes, const char *what) {
  (void)xalloc(1);                       /* the active block */
  OBlock *active = o_arena_blocks;
  if (!active || active->cap != O_ARENA_BLOCK) { fprintf(stderr, "%s: no standard block\n", what); return 1; }
  size_t used = active->used;

  char *big = (char *)xalloc(big_bytes);
  if (!big) { fprintf(stderr, "%s: no dedicated allocation\n", what); return 2; }
  if (o_arena_blocks != active) { fprintf(stderr, "%s: the dedicated block displaced the active one\n", what); return 3; }
  if (active->used != used) { fprintf(stderr, "%s: it was carved from the active block\n", what); return 4; }

  (void)xalloc(1);                       /* must land in the SAME block */
  if (o_arena_blocks != active) { fprintf(stderr, "%s: a small allocation started a new block\n", what); return 5; }
  if (active->used <= used) { fprintf(stderr, "%s: the small allocation went somewhere else\n", what); return 6; }

  /* BOTH ENDS OF THE SPAN ARE WRITTEN AND READ BACK, through a volatile pointer
     so the pair is not folded away at -O1: a store the compiler can prove
     nothing observes, followed by a load it can constant-fold, would make this
     assertion true without touching the allocation at all.

     WHAT IT ESTABLISHES, NARROWLY. The returned pointer is backed by writable
     memory at offset 0 and at big_bytes-1. It is NOT a bounds check: halving the
     dedicated block's capacity was tried and this still passed, because writing
     past a calloc'd region round-trips perfectly well without a sanitizer. An
     under-allocated span is exactly the class no gate here can see. */
  {
    volatile char *span = (volatile char *)big;
    span[0] = 'a';
    span[big_bytes - 1] = 'z';
    if (span[0] != 'a' || span[big_bytes - 1] != 'z') {
      fprintf(stderr, "%s: the dedicated span is not writable end to end\n", what);
      return 7;
    }
  }
  o_arena_release();
  return 0;
}

int main(void) {
  int rc = probe(O_ARENA_BLOCK, "exactly one block");
  if (rc) return rc;
  rc = probe(O_ARENA_BLOCK * 2 + 1, "more than one block");
  if (rc) return rc + 10;
  fputs("ok\n", stdout);
  return 0;
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rt.c"), []byte(llvmRuntimeC), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.c"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "driver")
	cc := exec.Command("clang", "-g", "-O1", "-o", bin, "driver.c")
	cc.Dir = dir
	if b, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("the driver did not compile, so nothing below ran: %v\n%s", err, b)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("the block-shape driver failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("the driver did not reach its own verdict, so its silence is not evidence:\n%s", out)
	}
}

// THE RELEASE POINT IS AFTER SERIALISATION, AND ORDER IS THE WHOLE CLAIM (#165).
//
// An arena that is released is not, by itself, worth anything: releasing it at
// the wrong moment hands `o_print` freed memory, and releasing it nowhere is the
// leak the design set out to name. Both are single-line mistakes in the emitter
// and neither changes any other assertion in this package — the compiled corpus
// agrees with `oath eval` either way, because a freed arena's bytes are usually
// still readable and a leaked one is never observed by a program that exits.
//
// So this witnesses the ORDER inside `main`: the answer is serialised, THEN the
// region that produced it is released, THEN main returns. The universe is the
// emitted main function rather than the whole module, because that is where the
// entry protocol's sequence lives; a handler entry will have its own release at
// its own octet boundary, and this test will not be the one that checks it.
func TestEmittedMainReleasesTheArenaAfterSerialising(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn echo [] [(args (List Str))] Str
		(match args ((Nil) "none") ((Cons h t) h)))`)
	markVerified(t, st, "echo")
	prog, err := planProgram(st, "echo")
	if err != nil {
		t.Fatal(err)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		t.Fatalf("emitLLVM(echo): %v", err)
	}

	// THE CALL MUST BE LINKABLE, WHICH IS TWO FACTS, NOT ONE. A call to an
	// undeclared function is not valid IR, and a declared function with no
	// definition fails at link — and this test does not invoke clang, so neither
	// would surface here. They are asserted rather than inherited.
	if !strings.Contains(ir, "declare void @o_arena_release()") {
		t.Error("the emitted module does not declare @o_arena_release, so the call below " +
			"could not be assembled")
	}
	if !strings.Contains(llvmRuntimeC, "void o_arena_release(void)") {
		t.Error("the runtime does not define o_arena_release, so a module calling it would " +
			"fail at link rather than release anything")
	}

	i := strings.Index(ir, "define i32 @main(")
	if i < 0 {
		t.Fatal("the emitted module has no main, so this test is reading nothing")
	}
	main := ir[i:]

	// EXACTLY ONE OF EACH, because the ordering comparison below is meaningless
	// over two candidates: with a second print or a second release, "the release
	// follows the print" would be true of some pair and say nothing about the
	// pair that runs.
	if n := strings.Count(main, "call void @o_print("); n != 1 {
		t.Fatalf("main serialises %d times, want exactly 1; the ordering assertion below "+
			"does not mean anything over several", n)
	}
	if n := strings.Count(main, "call void @o_arena_release()"); n != 1 {
		t.Fatalf("main releases the request arena %d times, want exactly 1. Zero is the leak "+
			"this design exists to remove; more than one is a double free", n)
	}

	print, release := strings.Index(main, "call void @o_print("), strings.Index(main, "call void @o_arena_release()")
	ret := strings.Index(main, "ret i32 0")
	if ret < 0 {
		t.Fatal("main has no `ret i32 0`, so `before main returns` cannot be checked here")
	}
	if release < print {
		t.Error("main releases the request arena BEFORE serialising the answer. o_print reads " +
			"the value's buffer, so this hands it freed memory — and the release is sound " +
			"only because it happens after the answer has become octets")
	}
	if release > ret {
		t.Error("main returns before releasing the request arena, so the region outlives the " +
			"request that owns it and the release point is decorative")
	}
}

type staticVar struct{ name, decl string }

// arenaOwnershipRoot reports whether a declaration is EXACTLY the arena's
// ownership root — the one object of static storage duration this runtime is
// allowed, and the one whose clearing at release is separately witnessed.
//
// KEYED ON THE WHOLE DECLARATION, NOT ON THE NAME, because the permission is not
// transferable. It was argued for a root that o_arena_release traverses and
// clears; `static OVal *o_arena_blocks` is a bare value slot that nothing
// clears, and matching on the name alone would hand it an argument nobody made
// about it. Whitespace is normalised and comments dropped so the match survives
// reformatting, and nothing else is: an unrecognised spelling fails closed,
// which for an allowlist means the declaration is REPORTED rather than admitted.
func arenaOwnershipRoot(v staticVar) bool {
	return v.name == "o_arena_blocks" && normalizeDecl(v.decl) == "static OBlock *o_arena_blocks;"
}

// requestContinuationSlot reports whether a declaration is EXACTLY one of the
// refusal boundary's two slots: the in-flight flag and the jump target SPEC
// §14.2's 500-and-keep-serving disposition needs (see this file's header).
//
// KEYED ON THE WHOLE DECLARATION for the reason arenaOwnershipRoot gives, and
// with one addition that is the entire argument for these two: the permitted
// types carry NO POINTER, so neither can be the slot that outlives a release
// holding request memory. A `*` in either spelling is a different declaration
// and is reported, which is why the type is matched rather than the name.
func requestContinuationSlot(v staticVar) bool {
	switch normalizeDecl(v.decl) {
	case "static int o_request_live;":
		return v.name == "o_request_live"
	case "static jmp_buf o_request_jump;":
		return v.name == "o_request_jump"
	}
	return false
}

// processLifetimeSlot reports whether a declaration is EXACTLY one of the
// process-lifetime region's two: its block list and its phase (#173).
//
// KEYED ON THE WHOLE DECLARATION, like both predicates above. What differs is
// the ARGUMENT the exemption rests on, and it is not the arena's: this root is
// never cleared, so "the retention ends" is unavailable and would be false. What
// holds instead is that the retention never BEGINS for a request — the region is
// carved from only while o_perm_state is 1, which is reached once before any
// listener exists and left, one-way, before the serve loop is entered.
//
// That makes the phase slot load-bearing rather than incidental, which is why it
// is permitted here rather than waved through as "just an int": it is the whole
// of the argument for the pointer above it. Both halves are witnessed by
// TestProcessLifetimeRegionOutlivesTheArena — the region is not what
// o_arena_release frees, and the loop cannot run before the region is sealed.
func processLifetimeSlot(v staticVar) bool {
	switch normalizeDecl(v.decl) {
	case "static OBlock *o_perm_blocks;":
		return v.name == "o_perm_blocks"
	case "static int o_perm_state;":
		return v.name == "o_perm_state"
	}
	return false
}

func normalizeDecl(decl string) string {
	if i := strings.Index(decl, "/*"); i >= 0 {
		decl = decl[:i]
	}
	if i := strings.Index(decl, "//"); i >= 0 {
		decl = decl[:i]
	}
	return strings.Join(strings.Fields(decl), " ")
}

// staticVars finds declarations with STATIC STORAGE DURATION in C source, at any
// indentation: `static <type> <name>` that is not a function. A scanner rather
// than a parser, which is why the callers inject before trusting its silence.
// stackGuardSlot reports whether a declaration is EXACTLY one of the stack
// guard's three: the floor the emitted IR loads at every function entry, the
// budget the diagnostic reports, and whether that budget was MEASURED from an
// rlimit or fell back to a constant.
//
// KEYED ON THE WHOLE DECLARATION, like all three predicates above. The ARGUMENT
// differs again, and it is the simplest of the four: NONE OF THESE TYPES CAN
// HOLD A POINTER. uintptr_t, size_t and int are integers, so no capability can
// leave request memory in one — which is the hazard this test exists to prevent,
// and it is unreachable here by the type rather than by a lifetime argument that
// would have to be re-made. That is why the type is matched and not only the
// name: a `*` in any of these spellings is a different declaration and is
// reported.
//
// o_stack_floor is deliberately NOT static — the emitted IR loads it as an
// external global — so it reaches this scanner through the file-scope half of
// its universe rather than the keyword half. It is exempt on the same argument.
func stackGuardSlot(v staticVar) bool {
	switch normalizeDecl(v.decl) {
	case "uintptr_t o_stack_floor;":
		return v.name == "o_stack_floor"
	case "static size_t o_stack_budget;":
		return v.name == "o_stack_budget"
	case "static int o_stack_from_rlimit;":
		return v.name == "o_stack_from_rlimit"
	}
	return false
}

func staticVars(src string) []staticVar {
	// `static` IS OPTIONAL HERE BECAUSE THE UNIVERSE BELOW ALREADY IS. This
	// scanner's population is "anything with the keyword at any indentation,
	// PLUS anything declared at file scope at all" — but the regex used to
	// require the keyword, so it could SEE a file-scope declaration without it
	// and not read it. Every such line came back with an empty name and was
	// reported as unparseable: a correct failure with a useless message, and one
	// that could not be exempted even with an argument, since an exemption keys
	// on a name the scanner never extracted.
	//
	// Nothing widens here: a line without the keyword had to be at file scope to
	// reach this point, and function declarators are removed before it.
	decl := regexp.MustCompile(`^(?:static\s+)?(?:const\s+)?[A-Za-z_][A-Za-z0-9_]*[\s*]+(?:volatile\s+)?\**([A-Za-z_][A-Za-z0-9_]*)\s*(?:=[^;]*)?(?:\[[^\]]*\])?\s*;`)
	strip := func(l string) string {
		if i := strings.Index(l, "/*"); i >= 0 {
			l = l[:i]
		}
		if i := strings.Index(l, "//"); i >= 0 {
			l = l[:i]
		}
		return strings.TrimRight(l, " \t")
	}
	var out []staticVar
	// BRACE DEPTH, NOT INDENTATION. C scope is not whitespace: a file-scope
	// declaration written with a leading space still has static storage duration,
	// and inferring scope from the left margin missed it.
	inComment := false
	for _, raw := range strings.Split(src, "\n") {
		// BLOCK COMMENTS SPAN LINES, and their interiors are ordinary prose that
		// no declaration regex can read. Without this, every continuation line of
		// a multi-line comment was reported as an unparsable declaration — the
		// fail-closed default turning into noise, which is its own way of being
		// ignored.
		line := raw
		if inComment {
			if i := strings.Index(line, "*/"); i >= 0 {
				line, inComment = line[i+2:], false
			} else {
				continue
			}
		}
		for {
			i := strings.Index(line, "/*")
			if i < 0 {
				break
			}
			if j := strings.Index(line[i:], "*/"); j >= 0 {
				line = line[:i] + line[i+j+2:]
				continue
			}
			line, inComment = line[:i], true
			break
		}
		stripped := strip(line)
		l := strings.TrimLeft(stripped, " \t")
		// A FUNCTION HAS ITS PARENTHESIS BEFORE ANY `=`; AN OBJECT MAY HAVE ONE
		// AFTER. Skipping every parenthesised line let `static OVal *stash =
		// (OVal *)0;` through — a cast in the initialiser, which is ordinary C
		// and is storage all the same. Only the declarator is tested.
		declarator := l
		if i := strings.Index(l, "="); i >= 0 {
			declarator = l[:i]
		}
		// A FUNCTION'S PARENTHESIS FOLLOWS AN IDENTIFIER; AN OBJECT'S DOES NOT.
		// `cap_env_code(OVal **env` is a function. `static OVal *(stash);` is a
		// parenthesised declarator — valid C, an ordinary pointer object, and a
		// place to retain — and treating every parenthesis as a function skipped
		// it. This is a scanner, not a C parser, and that is why an unrecognised
		// `static` line FAILS rather than passing: the forms it cannot read are
		// reported instead of vanishing.
		isFunc := false
		// An ARRAY BOUND may contain a parenthesised expression —
		// `static OVal *stash[sizeof(OVal *)];` — and `sizeof` is an identifier,
		// so the identifier-before-paren rule alone called it a function. A `[`
		// ahead of the first `(` settles it: that is a declarator, not a
		// parameter list.
		if b := strings.Index(declarator, "["); b >= 0 {
			if pi := strings.Index(declarator, "("); pi < 0 || b < pi {
				declarator = declarator[:b]
			}
		}
		if i := strings.Index(declarator, "("); i > 0 {
			c := declarator[i-1]
			isFunc = c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		}
		if l == "" || isFunc || strings.HasPrefix(l, "typedef") ||
			strings.HasPrefix(l, "#") || strings.HasPrefix(l, "}") {
			continue
		}
		// A TYPE DEFINITION IS NOT STORAGE. `enum { ... };` and `struct OVal {`
		// declare types and allocate nothing; `struct Holder stash;` declares an
		// OBJECT and does. The brace is what separates them, so it is the test —
		// not the leading keyword, which both share.
		if strings.Contains(l, "{") &&
			(strings.HasPrefix(l, "enum") || strings.HasPrefix(l, "struct") || strings.HasPrefix(l, "union")) {
			continue
		}
		// STATIC STORAGE DURATION, not the `static` KEYWORD. A file-scope object
		// gets it without the keyword — `OVal *stash;` at column 0 is persistent
		// mutable storage — so the universe is: anything with the keyword at any
		// indentation, plus anything declared at file scope at all.
		// SCOPE BY INDENTATION, WITH THE LIMIT STATED. Brace counting was tried
		// and made this worse: multi-line function signatures and block comments
		// both look like file-scope declarations to a line scanner, so the
		// fail-closed default turned into noise. A KNOWN, NARROW gap beats an
		// unbounded one — a file-scope declaration written with leading
		// whitespace is missed here, and the emitted runtime writes none.
		atFileScope := stripped == strings.TrimLeft(stripped, " \t")
		// `static` ANYWHERE IN THE QUALIFIERS, not only as a prefix.
		// `_Thread_local static OVal *stash;` is block-scoped, outlives the call
		// exactly as a plain function-local static does, and does not begin with
		// the keyword.
		hasStatic := strings.HasPrefix(l, "static ") || strings.Contains(l, " static ")
		if !hasStatic && !atFileScope {
			continue
		}
		// A NON-POINTER OBJECT CANNOT HOLD ARENA MEMORY. A future `static const
		// o_u32 masks[]` lookup table is storage, but nothing about it can retain
		// a request pointer, and failing it would make this test obstruct
		// unrelated work while defending nothing. The line is the pointer, not
		// the qualifier: `static const char *volatile o_kept` WAS const and was
		// exactly the hazard.
		if !strings.Contains(declarator, "*") && strings.Contains(l, "const") {
			continue
		}
		// EVERY non-function `static` line produces an entry. A line the regex
		// cannot read yields name "" so the caller REPORTS it — skipping it here
		// is what made `static unsigned char *stash;` vanish, and a scanner that
		// drops what it does not understand reports a clean runtime forever.
		if m := decl.FindStringSubmatch(l); m != nil {
			out = append(out, staticVar{name: m[1], decl: strings.TrimSpace(raw)})
		} else {
			out = append(out, staticVar{name: "", decl: strings.TrimSpace(raw)})
		}
	}
	return out
}
