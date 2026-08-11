//! Bridge obligations (SPEC §7.4) — the `List Int` ↔ `(Seq Int)` bridge.
//!
//! §7.4 pins the BYTES of eleven SMT obligations and nothing else. This module
//! is a pure text producer: it never runs a solver, so it is not behind the
//! `prove` feature and it builds for wasm32 unchanged. §7.4 is explicitly
//! OUTSIDE §10's conformance surface — a kernel emitting no bridge obligation
//! is not deficient — but a kernel that DOES emit one must emit exactly these
//! bytes, which is the whole content of the section.
//!
//! There are TWO FAMILIES and they do NOT share a preamble (§7.4.1):
//!
//!   * the CARRIER family (§7.4.2, §7.4.3) — `to-seq` and `of-seq` are mutually
//!     inverse — whose complete script is the §7.4.1 [`CORE`] followed by its
//!     subgoal block;
//!   * the TRANSPORT family (§7.4.4-§7.4.8) — one bridged function commutes
//!     with `to-seq` — whose complete script is the §7.4.4
//!     [`TRANSPORT_PREAMBLE`], then the bridged function's declaration block,
//!     then the subgoal block, concatenated in that order with no separator.
//!
//! The digest in the manifest is over the COMPLETE script in both families;
//! only what "complete" concatenates differs.

use crate::hash::sha256_hex;

/// §7.4.1 "The core" — the CARRIER family's preamble, verbatim, one LF after
/// each line. It is NOT the transport family's; see [`TRANSPORT_PREAMBLE`].
///
/// Three byte-level details §7.4.1 flags as load-bearing and that a "tidying"
/// kernel would silently break:
///   * the SPACE after the nullary constructor name, `(Nil_List_Int )`, which
///     is §7.1's existing datatype spelling;
///   * `(as seq.empty (Seq Int))` keeps its `as` annotation — `seq.empty` takes
///     no arguments, so its range sort is otherwise ambiguous;
///   * symbol names are structural per §7.2 — `fn_<name>_<sorts>`, `p<i>`/`q<i>`
///     for binders by index, `s0` for the sequence binder.
///
/// Both bridge functions are DEFINED, not merely declared (a soundness
/// requirement, not a style): an uninterpreted `of-seq` constrained only by
/// implications is an arbitrary function, and the round-trip obligations would
/// then be satisfiable when negated for reasons having nothing to do with the
/// encoding.
pub const CORE: &str = concat!(
    "(declare-datatypes ((List_Int 0)) (((Nil_List_Int ) (Cons_List_Int (Cons_List_Int_0 Int) (Cons_List_Int_1 List_Int)))))\n",
    "(declare-fun fn_to_seq_Int (List_Int) (Seq Int))\n",
    "(assert (forall ((p0 List_Int)) (! (= (fn_to_seq_Int p0) (ite ((_ is Nil_List_Int) p0) (as seq.empty (Seq Int)) (seq.++ (seq.unit (Cons_List_Int_0 p0)) (fn_to_seq_Int (Cons_List_Int_1 p0))))) :pattern ((fn_to_seq_Int p0)))))\n",
    "(assert (= (fn_to_seq_Int Nil_List_Int) (as seq.empty (Seq Int))))\n",
    "(assert (forall ((q0 Int) (q1 List_Int)) (= (fn_to_seq_Int (Cons_List_Int q0 q1)) (seq.++ (seq.unit q0) (fn_to_seq_Int q1)))))\n",
    "(declare-fun fn_of_seq_Int ((Seq Int)) List_Int)\n",
    "(assert (forall ((s0 (Seq Int))) (! (= (fn_of_seq_Int s0) (ite (= (seq.len s0) 0) Nil_List_Int (Cons_List_Int (seq.nth s0 0) (fn_of_seq_Int (seq.extract s0 1 (- (seq.len s0) 1)))))) :pattern ((fn_of_seq_Int s0)))))\n",
);

/// §7.4.4 "The transport preamble" — the TRANSPORT family's preamble, emitted
/// INSTEAD OF [`CORE`], not in addition to it.
///
/// It is §7.4.1's core minus `of-seq` (no transport goal mentions it) and minus
/// the patterned `ite`-form equation. That equation exists to unfold `to-seq` at
/// an argument that is not syntactically a constructor application, which only
/// the carrier round-trips produce; it is inert for a transport goal and an
/// inert axiom is not free, because its trigger matches every `to-seq` term a
/// transport goal builds.
///
/// `to-seq` is still DEFINED and not merely declared: the two per-constructor
/// equations pin it uniquely on the datatype's finite elements, which is what
/// makes the extension conservative.
pub const TRANSPORT_PREAMBLE: &str = concat!(
    "(declare-datatypes ((List_Int 0)) (((Nil_List_Int ) (Cons_List_Int (Cons_List_Int_0 Int) (Cons_List_Int_1 List_Int)))))\n",
    "(declare-fun fn_to_seq_Int (List_Int) (Seq Int))\n",
    "(assert (= (fn_to_seq_Int Nil_List_Int) (as seq.empty (Seq Int))))\n",
    "(assert (forall ((q0 Int) (q1 List_Int)) (= (fn_to_seq_Int (Cons_List_Int q0 q1)) (seq.++ (seq.unit q0) (fn_to_seq_Int q1)))))\n",
);

/// §7.4.3 "The measure obligation". The `seq.len` scheme of §7.4.2 is sound only
/// if the measure strictly decreases; this is emitted as its OWN obligation —
/// its own SCRIPT, appended to the §7.4.1 core exactly like the other two — and
/// MUST NOT be asserted as a hypothesis inside `roundtrip2-step`. Asserted into
/// the step it would be a supplied lemma, and the step would then prove nothing
/// about whether the recursion terminates. A kernel whose solver answers
/// anything but `unsat` here MUST NOT report the round-trip as established,
/// however the other two subgoals answered.
///
/// The core is redundant for this particular goal, which mentions neither bridge
/// function; §7.4.3 includes it anyway so that every CARRIER obligation has one
/// shape.
const MEASURE_DECREASES: &str = concat!(
    "(declare-const s (Seq Int))\n",
    "(assert (> (seq.len s) 0))\n",
    "(assert (not (= (seq.len (seq.extract s 1 (- (seq.len s) 1))) (- (seq.len s) 1))))\n",
    "(check-sat)\n",
);

/// §7.4.2, base case of the `seq.len` induction scheme.
///
/// The name is not a dangling reference: a datatype/sequence bijection has two
/// carrier round-trips and §7.4.2 pins only the second, `∀s. to-seq(of-seq s) =
/// s`. The first inducts structurally over `List`, which §7.2 already supplies.
const ROUNDTRIP2_BASE: &str = concat!(
    "(declare-const s (Seq Int))\n",
    "(assert (= (seq.len s) 0))\n",
    "(assert (not (= (fn_to_seq_Int (fn_of_seq_Int s)) s)))\n",
    "(check-sat)\n",
);

/// §7.4.2, step case: the sequence-sorted analogue of §7.2's recursion
/// induction — induct along the function's own recursion, with the hypothesis
/// at the argument the recursive call is made on.
const ROUNDTRIP2_STEP: &str = concat!(
    "(declare-const s (Seq Int))\n",
    "(assert (> (seq.len s) 0))\n",
    "(define-fun ih_tail () (Seq Int) (seq.extract s 1 (- (seq.len s) 1)))\n",
    "(assert (= (fn_to_seq_Int (fn_of_seq_Int ih_tail)) ih_tail))\n",
    "(assert (not (= (fn_to_seq_Int (fn_of_seq_Int s)) s)))\n",
    "(check-sat)\n",
);

/// §7.4.5 `append`'s declaration block:
/// `∀ xs ys. to-seq(append xs ys) = (seq.++ (to-seq xs) (to-seq ys))`,
/// inducting on `xs` and generalizing `ys`.
const DECL_APPEND: &str = concat!(
    "(declare-fun fn_append_Int (List_Int List_Int) List_Int)\n",
    "(assert (forall ((p0 List_Int) (p1 List_Int)) (! (= (fn_append_Int p0 p1) (ite ((_ is Nil_List_Int) p0) p1 (Cons_List_Int (Cons_List_Int_0 p0) (fn_append_Int (Cons_List_Int_1 p0) p1)))) :pattern ((fn_append_Int p0 p1)))))\n",
);

/// §7.4.5 `transport-append-base`. `b0` is declared and unused: §7.4.4 requires
/// ALL of the obligation's binders as fresh constants in parameter order,
/// including the one the induction replaces, because declaring the binders and
/// substituting into the formula are separate steps in §7.2.
const TRANSPORT_APPEND_BASE: &str = concat!(
    "(declare-const b0 List_Int)\n",
    "(declare-const b1 List_Int)\n",
    "(assert (not (= (fn_to_seq_Int (fn_append_Int Nil_List_Int b1)) (seq.++ (fn_to_seq_Int Nil_List_Int) (fn_to_seq_Int b1)))))\n",
    "(check-sat)\n",
);

/// §7.4.5 `transport-append-step`. The hypothesis generalizes every binder other
/// than the one being inducted on, quantified at the index it has in the
/// obligation — here `ys` is `b1`, so the bound variable is `q1`.
const TRANSPORT_APPEND_STEP: &str = concat!(
    "(declare-const b0 List_Int)\n",
    "(declare-const b1 List_Int)\n",
    "(declare-const f0 Int)\n",
    "(declare-const f1 List_Int)\n",
    "(assert (forall ((q1 List_Int)) (= (fn_to_seq_Int (fn_append_Int f1 q1)) (seq.++ (fn_to_seq_Int f1) (fn_to_seq_Int q1)))))\n",
    "(assert (not (= (fn_to_seq_Int (fn_append_Int (Cons_List_Int f0 f1) b1)) (seq.++ (fn_to_seq_Int (Cons_List_Int f0 f1)) (fn_to_seq_Int b1)))))\n",
    "(check-sat)\n",
);

/// §7.4.6 `length`'s declaration block: `∀ xs. length(xs) = (seq.len (to-seq
/// xs))` — the one transport that lands in `Int` rather than in a sequence.
const DECL_LENGTH: &str = concat!(
    "(declare-fun fn_length_Int (List_Int) Int)\n",
    "(assert (forall ((p0 List_Int)) (! (= (fn_length_Int p0) (ite ((_ is Nil_List_Int) p0) 0 (+ 1 (fn_length_Int (Cons_List_Int_1 p0))))) :pattern ((fn_length_Int p0)))))\n",
);

/// §7.4.6 `transport-length-base`.
const TRANSPORT_LENGTH_BASE: &str = concat!(
    "(declare-const b0 List_Int)\n",
    "(assert (not (= (fn_length_Int Nil_List_Int) (seq.len (fn_to_seq_Int Nil_List_Int)))))\n",
    "(check-sat)\n",
);

/// §7.4.6 `transport-length-step`. `length` has a single binder, so the
/// induction hypothesis generalizes nothing and carries NO quantifier — the
/// general rule applied, not a special case.
const TRANSPORT_LENGTH_STEP: &str = concat!(
    "(declare-const b0 List_Int)\n",
    "(declare-const f0 Int)\n",
    "(declare-const f1 List_Int)\n",
    "(assert (= (fn_length_Int f1) (seq.len (fn_to_seq_Int f1))))\n",
    "(assert (not (= (fn_length_Int (Cons_List_Int f0 f1)) (seq.len (fn_to_seq_Int (Cons_List_Int f0 f1))))))\n",
    "(check-sat)\n",
);

/// §7.4.7 `take`'s declaration block: `∀ k xs. to-seq(take k xs) = (seq.extract
/// s 0 c)`. `k` is the FIRST parameter of `take` in Oath, so it is `b0` and the
/// list is `b1`.
const DECL_TAKE: &str = concat!(
    "(declare-fun fn_take_Int (Int List_Int) List_Int)\n",
    "(assert (forall ((p0 Int) (p1 List_Int)) (! (= (fn_take_Int p0 p1) (ite (<= p0 0) Nil_List_Int (ite ((_ is Nil_List_Int) p1) Nil_List_Int (Cons_List_Int (Cons_List_Int_0 p1) (fn_take_Int (- p0 1) (Cons_List_Int_1 p1)))))) :pattern ((fn_take_Int p0 p1)))))\n",
);

/// §7.4.7 `transport-take-base`. The index is CLAMPED — `(ite (< k 0) 0 (ite (>
/// k (seq.len s)) (seq.len s) k))` — which is a soundness requirement and not a
/// convenience: `take`/`drop` are TOTAL in Oath at every `k` while `seq.extract`
/// is not, and a guarded equation is not available because an obligation is a
/// global fact about the bridged function.
const TRANSPORT_TAKE_BASE: &str = concat!(
    "(declare-const b0 Int)\n",
    "(declare-const b1 List_Int)\n",
    "(define-fun s_nil () (Seq Int) (fn_to_seq_Int Nil_List_Int))\n",
    "(assert (not (= (fn_to_seq_Int (fn_take_Int b0 Nil_List_Int)) (seq.extract s_nil 0 (ite (< b0 0) 0 (ite (> b0 (seq.len s_nil)) (seq.len s_nil) b0))))))\n",
    "(check-sat)\n",
);

/// §7.4.7 `transport-take-step`. The clamp inside the induction hypothesis is
/// written out longhand over the bound `q0`, because a `define-fun` cannot
/// mention a bound variable; the sequence itself is still `s_tail`, which names
/// only the declared constant `f1`.
const TRANSPORT_TAKE_STEP: &str = concat!(
    "(declare-const b0 Int)\n",
    "(declare-const b1 List_Int)\n",
    "(declare-const f0 Int)\n",
    "(declare-const f1 List_Int)\n",
    "(define-fun s_tail () (Seq Int) (fn_to_seq_Int f1))\n",
    "(define-fun s_cons () (Seq Int) (fn_to_seq_Int (Cons_List_Int f0 f1)))\n",
    "(assert (forall ((q0 Int)) (= (fn_to_seq_Int (fn_take_Int q0 f1)) (seq.extract s_tail 0 (ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0))))))\n",
    "(assert (not (= (fn_to_seq_Int (fn_take_Int b0 (Cons_List_Int f0 f1))) (seq.extract s_cons 0 (ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0))))))\n",
    "(check-sat)\n",
);

/// §7.4.8 `drop`'s declaration block: `∀ k xs. to-seq(drop k xs) = (seq.extract
/// s c (- (seq.len s) c))` with the same `s` and `c` as §7.4.7.
const DECL_DROP: &str = concat!(
    "(declare-fun fn_drop_Int (Int List_Int) List_Int)\n",
    "(assert (forall ((p0 Int) (p1 List_Int)) (! (= (fn_drop_Int p0 p1) (ite (<= p0 0) p1 (ite ((_ is Nil_List_Int) p1) Nil_List_Int (fn_drop_Int (- p0 1) (Cons_List_Int_1 p1))))) :pattern ((fn_drop_Int p0 p1)))))\n",
);

/// §7.4.8 `transport-drop-base`. The clamp appears TWICE in each formula — once
/// as the offset and once inside the length — and BOTH occurrences are written
/// out; §7.4.8 says a kernel that factored one of them into a `define-fun` would
/// emit different bytes.
const TRANSPORT_DROP_BASE: &str = concat!(
    "(declare-const b0 Int)\n",
    "(declare-const b1 List_Int)\n",
    "(define-fun s_nil () (Seq Int) (fn_to_seq_Int Nil_List_Int))\n",
    "(assert (not (= (fn_to_seq_Int (fn_drop_Int b0 Nil_List_Int)) (seq.extract s_nil (ite (< b0 0) 0 (ite (> b0 (seq.len s_nil)) (seq.len s_nil) b0)) (- (seq.len s_nil) (ite (< b0 0) 0 (ite (> b0 (seq.len s_nil)) (seq.len s_nil) b0)))))))\n",
    "(check-sat)\n",
);

/// §7.4.8 `transport-drop-step`.
const TRANSPORT_DROP_STEP: &str = concat!(
    "(declare-const b0 Int)\n",
    "(declare-const b1 List_Int)\n",
    "(declare-const f0 Int)\n",
    "(declare-const f1 List_Int)\n",
    "(define-fun s_tail () (Seq Int) (fn_to_seq_Int f1))\n",
    "(define-fun s_cons () (Seq Int) (fn_to_seq_Int (Cons_List_Int f0 f1)))\n",
    "(assert (forall ((q0 Int)) (= (fn_to_seq_Int (fn_drop_Int q0 f1)) (seq.extract s_tail (ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0)) (- (seq.len s_tail) (ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0)))))))\n",
    "(assert (not (= (fn_to_seq_Int (fn_drop_Int b0 (Cons_List_Int f0 f1))) (seq.extract s_cons (ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0)) (- (seq.len s_cons) (ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0)))))))\n",
    "(check-sat)\n",
);

/// Which §7.4 family an obligation belongs to. The families do NOT share a
/// preamble, and the family is what decides which one an obligation carries.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Family {
    /// §7.4.2/§7.4.3 — script is [`CORE`] + subgoal.
    Carrier,
    /// §7.4.4-§7.4.8 — script is [`TRANSPORT_PREAMBLE`] + declarations + subgoal.
    Transport,
}

impl Family {
    /// The preamble this family's COMPLETE script begins with.
    pub fn preamble(self) -> &'static str {
        match self {
            Family::Carrier => CORE,
            Family::Transport => TRANSPORT_PREAMBLE,
        }
    }
}

/// One §7.4 obligation, split into the three parts §7.4.4 names. A carrier
/// obligation has no declaration block, so `decls` is empty for it — the
/// concatenation is uniform and only its parts differ, which is exactly what
/// §7.4.9 says about the digest ("the digest is over the COMPLETE script in both
/// families, which is the fact that does not vary; only what 'complete'
/// concatenates does").
pub struct Obligation {
    pub id: &'static str,
    pub family: Family,
    /// The bridged function's declaration block (§7.4.5-§7.4.8), or `""` for a
    /// carrier obligation, which has none.
    pub decls: &'static str,
    pub subgoal: &'static str,
}

impl Obligation {
    /// The COMPLETE script bytes: preamble, then declarations, then subgoal,
    /// concatenated with no separator, the final `(check-sat)` terminated by one
    /// LF and nothing after it.
    ///
    /// No rlimit header and no trailing `get-info` lines: §7.4.9 states the
    /// script bytes are these alone, matching §7.2's rule that runner options
    /// sit outside a script's identity. §7.4.9 also fixes no solver budget —
    /// a caller that RUNS these scripts chooses and must state its rlimit.
    pub fn script(&self) -> String {
        let mut s = String::with_capacity(
            self.family.preamble().len() + self.decls.len() + self.subgoal.len(),
        );
        s.push_str(self.family.preamble());
        s.push_str(self.decls);
        s.push_str(self.subgoal);
        s
    }

    /// `SHA-256` over the script bytes ALONE, as 64 lowercase hex characters.
    pub fn digest(&self) -> String {
        sha256_hex(self.script().as_bytes())
    }
}

const fn carrier(id: &'static str, subgoal: &'static str) -> Obligation {
    Obligation { id, family: Family::Carrier, decls: "", subgoal }
}

const fn transport(
    id: &'static str,
    decls: &'static str,
    subgoal: &'static str,
) -> Obligation {
    Obligation { id, family: Family::Transport, decls, subgoal }
}

/// §7.4.9 "Emission order": scheme soundness first, so that a reader meeting a
/// failure meets it before the results it invalidates; then the carriers; then
/// the transports in §7.4.5-§7.4.8's order.
///
/// The carriers come before the transports because a transport result is not
/// USABLE without them, not merely because they read first: the transports carry
/// a refutation back, and only the carriers say every sequence is the image of a
/// list, so a kernel whose carrier obligations did not all answer `unsat` MUST
/// NOT report a `sat` obtained over this encoding as refuting an Oath goal.
pub const OBLIGATIONS: [Obligation; 11] = [
    carrier("measure-decreases", MEASURE_DECREASES),
    carrier("roundtrip2-base", ROUNDTRIP2_BASE),
    carrier("roundtrip2-step", ROUNDTRIP2_STEP),
    transport("transport-append-base", DECL_APPEND, TRANSPORT_APPEND_BASE),
    transport("transport-append-step", DECL_APPEND, TRANSPORT_APPEND_STEP),
    transport("transport-length-base", DECL_LENGTH, TRANSPORT_LENGTH_BASE),
    transport("transport-length-step", DECL_LENGTH, TRANSPORT_LENGTH_STEP),
    transport("transport-take-base", DECL_TAKE, TRANSPORT_TAKE_BASE),
    transport("transport-take-step", DECL_TAKE, TRANSPORT_TAKE_STEP),
    transport("transport-drop-base", DECL_DROP, TRANSPORT_DROP_BASE),
    transport("transport-drop-step", DECL_DROP, TRANSPORT_DROP_STEP),
];

/// Look up an obligation by the id §7.4 gives it.
pub fn find(id: &str) -> Option<&'static Obligation> {
    OBLIGATIONS.iter().find(|o| o.id == id)
}

/// Every id in §7.4.9's emission order.
pub fn ids() -> Vec<&'static str> {
    OBLIGATIONS.iter().map(|o| o.id).collect()
}

/// The complete script bytes for one obligation (§7.4.9), or `None` if `id` is
/// not one §7.4 names.
pub fn script(id: &str) -> Option<String> {
    find(id).map(|o| o.script())
}

/// §7.4.9's manifest: a header line followed by one line per obligation in
/// §7.4.9's order, EVERY line including the header terminated by a single LF
/// (`0x0A`), with no trailing blank line.
///
/// The header is `#`, SPACE, `id`, TAB, `sha256(script)` — 19 bytes before its
/// terminating LF — and each subsequent line is the obligation's id, one TAB
/// (`0x09`), and the digest as exactly 64 LOWERCASE hex characters with no `0x`
/// prefix.
pub fn manifest() -> String {
    let mut out = String::new();
    out.push_str("# id\tsha256(script)\n");
    for o in OBLIGATIONS.iter() {
        out.push_str(o.id);
        out.push('\t');
        out.push_str(&o.digest());
        out.push('\n');
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn carrier_core_is_seven_lines_each_lf_terminated() {
        assert!(CORE.ends_with('\n'));
        assert_eq!(CORE.lines().count(), 7);
        // No line carries trailing whitespace of its own...
        for line in CORE.lines() {
            assert!(!line.ends_with(' '), "trailing space on {:?}", line);
        }
        // ...but the nullary constructor keeps its interior space (§7.4.1).
        assert!(CORE.contains("(Nil_List_Int )"));
    }

    #[test]
    fn transport_preamble_is_the_core_minus_two_lines() {
        // §7.4.4: "That is §7.4.1's core minus `of-seq`, which no transport goal
        // mentions, and minus the patterned `ite`-form equation."
        assert!(TRANSPORT_PREAMBLE.ends_with('\n'));
        assert_eq!(TRANSPORT_PREAMBLE.lines().count(), 4);
        assert!(!TRANSPORT_PREAMBLE.contains("fn_of_seq_Int"));
        assert!(!TRANSPORT_PREAMBLE.contains(":pattern"));
        assert!(TRANSPORT_PREAMBLE.contains("(Nil_List_Int )"));
        // Every one of its lines is a line of the core, in the core's order.
        let core: Vec<&str> = CORE.lines().collect();
        let mut at = 0usize;
        for line in TRANSPORT_PREAMBLE.lines() {
            let pos = core[at..]
                .iter()
                .position(|c| *c == line)
                .unwrap_or_else(|| panic!("not a core line, in order: {:?}", line));
            at += pos + 1;
        }
        // `to-seq` is still DEFINED: both per-constructor equations survive.
        assert!(TRANSPORT_PREAMBLE.contains("(assert (= (fn_to_seq_Int Nil_List_Int) (as seq.empty (Seq Int))))"));
        assert!(TRANSPORT_PREAMBLE.contains("(forall ((q0 Int) (q1 List_Int))"));
    }

    #[test]
    fn the_families_do_not_share_a_preamble() {
        assert_ne!(CORE, TRANSPORT_PREAMBLE);
        for o in OBLIGATIONS.iter() {
            let s = o.script();
            assert!(
                s.starts_with(o.family.preamble()),
                "{} does not begin with its family's preamble",
                o.id
            );
            match o.family {
                // A carrier script must NOT begin with the transport preamble
                // and vice versa. The transport preamble is not a prefix of the
                // core (line 3 differs), so these are genuinely exclusive.
                Family::Carrier => assert!(!s.starts_with(TRANSPORT_PREAMBLE), "{}", o.id),
                Family::Transport => assert!(!s.starts_with(CORE), "{}", o.id),
            }
        }
    }

    #[test]
    fn the_as_annotation_survives() {
        // §7.4.1: `seq.empty` takes no arguments, so the bare form is ambiguous.
        assert_eq!(CORE.matches("(as seq.empty (Seq Int))").count(), 2);
        assert!(!CORE.contains(" seq.empty)"));
        assert_eq!(TRANSPORT_PREAMBLE.matches("(as seq.empty (Seq Int))").count(), 1);
        assert!(!TRANSPORT_PREAMBLE.contains(" seq.empty)"));
    }

    #[test]
    fn measure_is_not_a_hypothesis_of_the_step() {
        // §7.4.3: the separation is the point. The step must not assert the
        // measure fact; if it did, the subgoals would still be unsat while no
        // longer implying the universal.
        let step = script("roundtrip2-step").unwrap();
        assert!(!step.contains("(= (seq.len (seq.extract s 1 (- (seq.len s) 1))) (- (seq.len s) 1))"));
    }

    #[test]
    fn measure_decreases_is_its_own_script_carrying_the_core() {
        // §7.4.3: "its own SCRIPT, appended to the §7.4.1 core exactly like the
        // other two, not a bare subgoal".
        let m = script("measure-decreases").unwrap();
        assert!(m.starts_with(CORE));
        assert!(!m.contains("fn_of_seq_Int (fn_of_seq_Int"));
    }

    #[test]
    fn emission_order_is_soundness_then_carriers_then_transports() {
        assert_eq!(
            ids(),
            [
                "measure-decreases",
                "roundtrip2-base",
                "roundtrip2-step",
                "transport-append-base",
                "transport-append-step",
                "transport-length-base",
                "transport-length-step",
                "transport-take-base",
                "transport-take-step",
                "transport-drop-base",
                "transport-drop-step",
            ]
        );
        // Three carriers (§7.4.2 "and no others") and eight transports
        // (§7.4.4 "those eight subgoals and no others").
        assert_eq!(OBLIGATIONS.iter().filter(|o| o.family == Family::Carrier).count(), 3);
        assert_eq!(OBLIGATIONS.iter().filter(|o| o.family == Family::Transport).count(), 8);
        // The carriers all precede the transports.
        let first_transport = OBLIGATIONS
            .iter()
            .position(|o| o.family == Family::Transport)
            .unwrap();
        assert!(OBLIGATIONS[..first_transport].iter().all(|o| o.family == Family::Carrier));
        assert!(OBLIGATIONS[first_transport..].iter().all(|o| o.family == Family::Transport));
    }

    #[test]
    fn every_script_ends_in_check_sat_and_one_lf() {
        for o in OBLIGATIONS.iter() {
            let s = o.script();
            assert!(s.ends_with("(check-sat)\n"), "{}", o.id);
            assert!(!s.ends_with("\n\n"), "{} has a trailing blank line", o.id);
            assert_eq!(s.matches("(check-sat)").count(), 1, "{}", o.id);
        }
        assert!(script("no-such-obligation").is_none());
        assert!(find("no-such-obligation").is_none());
    }

    #[test]
    fn a_transport_script_is_preamble_then_decls_then_subgoal() {
        // §7.4.4: "three parts concatenated in that order with no separator".
        for o in OBLIGATIONS.iter().filter(|o| o.family == Family::Transport) {
            assert!(!o.decls.is_empty(), "{} has no declaration block", o.id);
            let expect = format!("{}{}{}", TRANSPORT_PREAMBLE, o.decls, o.subgoal);
            assert_eq!(o.script(), expect, "{}", o.id);
        }
        // A carrier has no declaration block at all.
        for o in OBLIGATIONS.iter().filter(|o| o.family == Family::Carrier) {
            assert_eq!(o.decls, "", "{}", o.id);
            assert_eq!(o.script(), format!("{}{}", CORE, o.subgoal), "{}", o.id);
        }
    }

    #[test]
    fn each_transport_pair_shares_its_declaration_block() {
        for (base, step) in [
            ("transport-append-base", "transport-append-step"),
            ("transport-length-base", "transport-length-step"),
            ("transport-take-base", "transport-take-step"),
            ("transport-drop-base", "transport-drop-step"),
        ] {
            assert_eq!(find(base).unwrap().decls, find(step).unwrap().decls);
        }
        // The four blocks are distinct, so no pair is silently aliased.
        let mut decls: Vec<&str> = OBLIGATIONS
            .iter()
            .filter(|o| o.family == Family::Transport)
            .map(|o| o.decls)
            .collect();
        decls.sort_unstable();
        decls.dedup();
        assert_eq!(decls.len(), 4);
    }

    #[test]
    fn transport_binders_are_all_declared_including_the_unused_one() {
        // §7.4.4: `b<i>` for the obligation's own binders, in parameter order,
        // ALL of them, including the one the induction replaces.
        for (id, binders) in [
            ("transport-append-base", &["(declare-const b0 List_Int)", "(declare-const b1 List_Int)"][..]),
            ("transport-append-step", &["(declare-const b0 List_Int)", "(declare-const b1 List_Int)"][..]),
            ("transport-length-base", &["(declare-const b0 List_Int)"][..]),
            ("transport-length-step", &["(declare-const b0 List_Int)"][..]),
            ("transport-take-base", &["(declare-const b0 Int)", "(declare-const b1 List_Int)"][..]),
            ("transport-take-step", &["(declare-const b0 Int)", "(declare-const b1 List_Int)"][..]),
            ("transport-drop-base", &["(declare-const b0 Int)", "(declare-const b1 List_Int)"][..]),
            ("transport-drop-step", &["(declare-const b0 Int)", "(declare-const b1 List_Int)"][..]),
        ] {
            let sub = find(id).unwrap().subgoal;
            for b in binders {
                assert!(sub.contains(b), "{} missing {}", id, b);
            }
            // `take`/`drop` take `k` FIRST, so the list is `b1` there and `b0`
            // for `append`/`length`: a swap would change these sorts.
            assert!(!sub.contains("(declare-const b2 "), "{}", id);
        }
        // The binder the induction replaces is declared and then unused.
        assert_eq!(find("transport-append-base").unwrap().subgoal.matches("b0").count(), 1);
        assert_eq!(find("transport-length-step").unwrap().subgoal.matches("b0").count(), 1);
        assert_eq!(find("transport-take-step").unwrap().subgoal.matches("b1").count(), 1);
        assert_eq!(find("transport-drop-step").unwrap().subgoal.matches("b1").count(), 1);
    }

    #[test]
    fn take_and_drop_indexes_are_clamped_and_never_guarded() {
        // §7.4.4: the clamp is a soundness requirement. A guarded equation would
        // license an invalid rewrite on any goal passing an out-of-range index.
        for id in ["transport-take-base", "transport-take-step", "transport-drop-base", "transport-drop-step"] {
            let sub = find(id).unwrap().subgoal;
            assert!(sub.contains("(ite (< b0 0) 0 (ite (> b0 (seq.len"), "{}", id);
            assert!(sub.contains("(seq.extract "), "{}", id);
            assert!(!sub.contains("=>"), "{} looks guarded", id);
        }
        // §7.4.8: the clamp appears TWICE per formula in `drop` — offset and
        // length — and both are written out rather than factored into a
        // `define-fun`.
        let dbase = find("transport-drop-base").unwrap().subgoal;
        assert_eq!(dbase.matches("(ite (< b0 0) 0 (ite (> b0 (seq.len s_nil)) (seq.len s_nil) b0))").count(), 2);
        let dstep = find("transport-drop-step").unwrap().subgoal;
        assert_eq!(dstep.matches("(ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0))").count(), 2);
        assert_eq!(dstep.matches("(ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0))").count(), 2);
        // `take` writes it exactly once per formula.
        let tstep = find("transport-take-step").unwrap().subgoal;
        assert_eq!(tstep.matches("(ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0))").count(), 1);
        assert_eq!(tstep.matches("(ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0))").count(), 1);
    }

    #[test]
    fn the_length_hypothesis_carries_no_quantifier() {
        // §7.4.6: a single binder generalizes nothing — "the general rule
        // applied, not a special case".
        let step = find("transport-length-step").unwrap().subgoal;
        assert!(!step.contains("forall"));
        // Every other step DOES generalize, at the index the binder has.
        assert!(find("transport-append-step").unwrap().subgoal.contains("(forall ((q1 List_Int))"));
        assert!(find("transport-take-step").unwrap().subgoal.contains("(forall ((q0 Int))"));
        assert!(find("transport-drop-step").unwrap().subgoal.contains("(forall ((q0 Int))"));
    }

    #[test]
    fn define_funs_appear_only_where_seven_four_four_permits() {
        // §7.4.4: only §7.4.7 and §7.4.8 use `s_nil`/`s_tail`/`s_cons`.
        for o in OBLIGATIONS.iter() {
            let uses = o.subgoal.contains("s_nil") || o.subgoal.contains("s_tail") || o.subgoal.contains("s_cons");
            let expected = o.id.starts_with("transport-take-") || o.id.starts_with("transport-drop-");
            assert_eq!(uses, expected, "{}", o.id);
        }
        // ...and `ih_tail` only in §7.4.2's step.
        for o in OBLIGATIONS.iter() {
            assert_eq!(o.subgoal.contains("ih_tail"), o.id == "roundtrip2-step", "{}", o.id);
        }
    }

    #[test]
    fn manifest_shape_and_digests_agree_with_emit() {
        let m = manifest();
        let lines: Vec<&str> = m.lines().collect();
        assert_eq!(lines.len(), 1 + OBLIGATIONS.len());
        assert_eq!(lines[0], "# id\tsha256(script)");
        // §7.4.9 counts the header: 19 bytes before its terminating LF.
        assert_eq!(lines[0].len(), 19);
        assert!(m.ends_with('\n'));
        assert!(!m.ends_with("\n\n"), "no trailing blank line");
        for (i, o) in OBLIGATIONS.iter().enumerate() {
            let cols: Vec<&str> = lines[i + 1].split('\t').collect();
            assert_eq!(cols.len(), 2, "exactly one TAB on {:?}", lines[i + 1]);
            assert_eq!(cols[0], o.id);
            // The digest must be the hash of exactly what `--emit` prints.
            assert_eq!(cols[1], sha256_hex(script(o.id).unwrap().as_bytes()));
            assert_eq!(cols[1].len(), 64);
            assert!(cols[1].chars().all(|c| c.is_ascii_digit() || ('a'..='f').contains(&c)));
        }
        // All eleven digests differ: no two obligations are the same script.
        let mut ds: Vec<String> = OBLIGATIONS.iter().map(|o| o.digest()).collect();
        ds.sort();
        ds.dedup();
        assert_eq!(ds.len(), OBLIGATIONS.len());
    }

    #[test]
    fn digests_are_pinned() {
        // A regression witness with teeth. Every constant above was extracted
        // byte-for-byte from §7.4's code blocks when written; these digests
        // record that state, so any later "tidying" of the script text — a
        // normalised space, a dropped `as`, a reordered line, a carrier preamble
        // leaking into a transport script — fails here rather than silently
        // emitting different bytes under the same name, which is the one thing
        // §7.4 forbids.
        assert_eq!(
            manifest(),
            "# id\tsha256(script)\n\
             measure-decreases\td7901f4196d715a9a8c271e46cd8dcf6fb5a4c2a69d0ecd38908db3e66fb30c7\n\
             roundtrip2-base\tf65a4530b81e956325be88cb18a0b0a96e78cd2ab454b9c0d7fd73151933f16a\n\
             roundtrip2-step\t4cd6f824421311eb9858688a8a49842576920cfc3bf25b630d4c1b0254a32413\n\
             transport-append-base\t9b13bcf134449f8217d9692128d1a49de94560c5816bc99c212e0a11fa36472f\n\
             transport-append-step\td98ceef20ccdf14d38639a96604030bfb5507daf71a333872f20936e8f91c666\n\
             transport-length-base\t28e1542628a7af945a95158f3c663be90f41051a119e32107e05f3d3e5249d55\n\
             transport-length-step\te32ca3ba57f1a7c14959fb9dee50b71796d470a2a5510fb04ac2942ac3ad7884\n\
             transport-take-base\tf3240518e16f493e71ff6dac81489e89a791c3a1b9fc078da2b5eb718228fcdf\n\
             transport-take-step\t7cd7d70fe0a1315c9d46ef59bc017467eba3667d5899dc9e8569297625588ee7\n\
             transport-drop-base\tfb300a05b1ae066cb80fcbb0169009fb42b5f1b1615fe8c6ab08518543d42db6\n\
             transport-drop-step\tfe56f6bd6efe9471590c1ff6565a99b52bcb4c926b0d01dd12b4aa137c2a8dc2\n"
        );
    }

    #[test]
    fn scripts_are_pinned_by_length_too() {
        // A digest changes on any edit but says nothing about WHAT changed; a
        // byte length localises a stray space or a dropped line to one script.
        let lens: Vec<usize> = OBLIGATIONS.iter().map(|o| o.script().len()).collect();
        assert_eq!(lens, PIN_LENS);
    }

    const PIN_LENS: [usize; 11] = [1002, 974, 1105, 834, 1026, 700, 830, 945, 1229, 981, 1354];
}
