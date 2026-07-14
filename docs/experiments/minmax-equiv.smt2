; Equivalence of the interval-module mutation survivors: every survivor
; replaces (if (< a b) b a) with (if (<= a b) b a) inside a min/max.
; The two expressions differ only at a = b, where both branches return
; the same value. unsat = equivalent for all inputs.
(declare-const a Int)
(declare-const b Int)
(assert (or (not (= (ite (< a b) b a) (ite (<= a b) b a)))
            (not (= (ite (< a b) a b) (ite (<= a b) a b)))))
(check-sat)
