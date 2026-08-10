//! Bridge obligations (SPEC §7.4) — the `List Int` ↔ `(Seq Int)` bridge.
//!
//! §7.4 pins the BYTES of three SMT obligations and nothing else. This module
//! is a pure text producer: it never runs a solver, so it is not behind the
//! `prove` feature and it builds for wasm32 unchanged. §7.4 is explicitly
//! OUTSIDE §10's conformance surface — a kernel emitting no bridge obligation
//! is not deficient — but a kernel that DOES emit one must emit exactly these
//! bytes, which is the whole content of the section.

use crate::hash::sha256_hex;

/// §7.4.1 "The core". Every bridge obligation for `List Int` begins with this
/// preamble, verbatim, one LF after each line.
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

/// §7.4.3 "The measure obligation". The `seq.len` scheme of §7.4.2 is sound only
/// if the measure strictly decreases; this is emitted as its OWN obligation and
/// MUST NOT be asserted as a hypothesis inside `roundtrip2-step`. Asserted into
/// the step it would be a supplied lemma, and the step would then prove nothing
/// about whether the recursion terminates. A kernel whose solver answers
/// anything but `unsat` here MUST NOT report the round-trip as established,
/// however the other two subgoals answered.
const MEASURE_DECREASES: &str = concat!(
    "(declare-const s (Seq Int))\n",
    "(assert (> (seq.len s) 0))\n",
    "(assert (not (= (seq.len (seq.extract s 1 (- (seq.len s) 1))) (- (seq.len s) 1))))\n",
    "(check-sat)\n",
);

/// §7.4.2, base case of the `seq.len` induction scheme.
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

/// §7.4.4 "Emission order": `measure-decreases`, `roundtrip2-base`,
/// `roundtrip2-step` — scheme soundness first, so that a reader meeting a
/// failure meets it before the results it invalidates. Pairs each id with the
/// subgoal appended to the §7.4.1 core.
pub const OBLIGATIONS: [(&str, &str); 3] = [
    ("measure-decreases", MEASURE_DECREASES),
    ("roundtrip2-base", ROUNDTRIP2_BASE),
    ("roundtrip2-step", ROUNDTRIP2_STEP),
];

/// The complete script bytes for one obligation: the §7.4.1 core plus the
/// subgoal. §7.4.1 says EVERY bridge obligation begins with the preamble and
/// §7.4.4 says the digest is over "core plus subgoal", so `measure-decreases`
/// carries the core too even though it mentions neither bridge function.
///
/// No rlimit header and no trailing `get-info` lines: §7.4.4 states the script
/// bytes are these alone, matching §7.2's rule that runner options sit outside
/// a script's identity.
pub fn script(id: &str) -> Option<String> {
    OBLIGATIONS
        .iter()
        .find(|(name, _)| *name == id)
        .map(|(_, subgoal)| format!("{}{}", CORE, subgoal))
}

/// §7.4.4's manifest: the header line `# id<TAB>sha256(script)` followed by one
/// `<id><TAB><lowercase hex>` line per obligation in emission order, each
/// terminated by LF. The digest is SHA-256 over the script bytes alone.
pub fn manifest() -> String {
    let mut out = String::new();
    out.push_str("# id\tsha256(script)\n");
    for (id, subgoal) in OBLIGATIONS.iter() {
        let bytes = format!("{}{}", CORE, subgoal);
        out.push_str(id);
        out.push('\t');
        out.push_str(&sha256_hex(bytes.as_bytes()));
        out.push('\n');
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn core_is_seven_lines_each_lf_terminated() {
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
    fn the_as_annotation_survives() {
        // §7.4.1: `seq.empty` takes no arguments, so the bare form is ambiguous.
        assert_eq!(CORE.matches("(as seq.empty (Seq Int))").count(), 2);
        assert!(!CORE.contains(" seq.empty)"));
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
    fn emission_order_is_soundness_first() {
        let ids: Vec<&str> = OBLIGATIONS.iter().map(|(i, _)| *i).collect();
        assert_eq!(ids, ["measure-decreases", "roundtrip2-base", "roundtrip2-step"]);
    }

    #[test]
    fn manifest_digests_the_script_bytes_alone() {
        let m = manifest();
        let lines: Vec<&str> = m.lines().collect();
        assert_eq!(lines.len(), 4);
        assert_eq!(lines[0], "# id\tsha256(script)");
        assert!(m.ends_with('\n'));
        for (i, (id, _)) in OBLIGATIONS.iter().enumerate() {
            let cols: Vec<&str> = lines[i + 1].split('\t').collect();
            assert_eq!(cols.len(), 2);
            assert_eq!(cols[0], *id);
            // The digest must be the hash of exactly what `--emit` prints.
            assert_eq!(cols[1], sha256_hex(script(id).unwrap().as_bytes()));
            assert_eq!(cols[1].len(), 64);
            assert!(cols[1].chars().all(|c| c.is_ascii_digit() || ('a'..='f').contains(&c)));
        }
    }

    #[test]
    fn digests_are_pinned() {
        // A regression witness with teeth. The three constants above were
        // checked byte-for-byte against §7.4.1/§7.4.2/§7.4.3's code blocks when
        // written; these digests record that state, so any later "tidying" of
        // the script text — a normalised space, a dropped `as`, a reordered
        // line — fails here rather than silently emitting different bytes under
        // the same name, which is the one thing §7.4 forbids.
        assert_eq!(
            manifest(),
            "# id\tsha256(script)\n\
             measure-decreases\td7901f4196d715a9a8c271e46cd8dcf6fb5a4c2a69d0ecd38908db3e66fb30c7\n\
             roundtrip2-base\tf65a4530b81e956325be88cb18a0b0a96e78cd2ab454b9c0d7fd73151933f16a\n\
             roundtrip2-step\t4cd6f824421311eb9858688a8a49842576920cfc3bf25b630d4c1b0254a32413\n"
        );
    }

    #[test]
    fn every_script_carries_the_core_and_ends_in_check_sat() {
        for (id, _) in OBLIGATIONS.iter() {
            let s = script(id).unwrap();
            assert!(s.starts_with(CORE));
            assert!(s.ends_with("(check-sat)\n"));
        }
        assert!(script("no-such-obligation").is_none());
    }
}
