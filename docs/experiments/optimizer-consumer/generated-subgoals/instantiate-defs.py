"""Definition-only instantiation of a prover-emitted structural-induction subgoal.

ONE machinery for any emitted subgoal script, keyed off PARSED STRUCTURE rather
than line numbers -- the subject (`opt`) and the wrong-optimizer control (`bado`)
have different line layouts, and a line-indexed generator would silently apply the
wrong equation to one of them.

Every premise is a ground instance of a FUNCTION DEFINING EQUATION, produced by
substitution into the parsed emitted axiom -- never retyped. Quantified soundness
LEMMAS present in the emitted script are ignored entirely: none is instantiated.

Usage: instantiate-defs.py <emitted.smt2> <Add|Mul> <base|ext|checksimp> <out.smt2>
  base -- parent + inlined-result + branch instances only
  ext  -- adds fn_ev at the two optimized subterms (measured necessary)
  checksimp -- emit and run the simplifier self-check instead (expects unsat)
"""
import sys, re

def tokenize(s): return re.findall(r'\(|\)|[^\s()]+', s)

def parse(toks, i=0):
    if toks[i] == '(':
        out, i = [], i + 1
        while toks[i] != ')':
            n, i = parse(toks, i); out.append(n)
        return out, i + 1
    return toks[i], i + 1

def p1(s):
    return parse(tokenize(s))[0]

def render(n):
    return n if isinstance(n, str) else '(' + ' '.join(render(x) for x in n) + ')'

def subst(n, env):
    if isinstance(n, str): return env.get(n, n)
    return [subst(x, env) for x in n]

src, ctor, mode, out_path = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
lines = open(src).read().splitlines()

# ---- classify the emitted script by structure -------------------------------
dt_line = next(l for l in lines if l.startswith('(declare-datatypes'))
declare_funs   = [l for l in lines if l.startswith('(declare-fun')]
declare_consts = [l for l in lines if l.startswith('(declare-const')]
asserts        = [l for l in lines if l.startswith('(assert')]

def is_forall(l):
    t = p1(l)
    return isinstance(t[1], list) and t[1][0] == 'forall'

def forall_parts(l):
    fa = p1(l)[1]
    vs = [b[0] for b in fa[1]]
    eq = fa[2]
    if isinstance(eq, list) and eq[0] == '!': eq = eq[1]
    return vs, eq

# A DEFINING equation is a forall whose LHS is exactly (f v) for the bound v.
defs = {}
for l in asserts:
    if not is_forall(l): continue
    vs, eq = forall_parts(l)
    lhs = eq[1]
    if len(vs) == 1 and isinstance(lhs, list) and len(lhs) == 2 and lhs[1] == vs[0]:
        defs[lhs[0]] = (vs, eq)          # fn_ev, fn_opt/fn_bado
    # anything else (the 2-variable soundness lemmas) is DISCARDED

fn_names = [p1(l)[1] for l in declare_funs]
assert 'fn_ev' in defs, "no fn_ev defining equation found"
opt_fn = next(n for n in fn_names if n != 'fn_ev')
assert opt_fn in defs, "no defining equation for %s" % opt_fn
ev_v,  ev_eq  = defs['fn_ev']
opt_v, opt_eq = defs[opt_fn]

# ---- datatype table, for the selector/tester simplifier ---------------------
dt = p1(dt_line)
ctors = {}                                  # ctor -> [selector, ...]
for c in dt[2][0]:
    ctors[c[0]] = [f[0] for f in c[1:]]
sel_of = {s: (c, i) for c, ss in ctors.items() for i, s in enumerate(ss)}

def simp(n):
    """Datatype-only simplification: selector-on-constructor, tester-on-
    constructor, and ite folding. Every rule is a datatype axiom z3 applies
    itself; correctness is checked against z3 separately."""
    if isinstance(n, str): return n
    n = [simp(x) for x in n]
    if len(n) == 2 and isinstance(n[0], str) and n[0] in sel_of \
       and isinstance(n[1], list) and isinstance(n[1][0], str) and n[1][0] in ctors:
        c, i = sel_of[n[0]]
        if n[1][0] == c: return n[1][1 + i]
    if len(n) == 2 and isinstance(n[0], list) and n[0][:2] == ['_', 'is'] \
       and isinstance(n[1], list) and isinstance(n[1][0], str) and n[1][0] in ctors:
        return 'true' if n[1][0] == n[0][2] else 'false'
    if len(n) == 4 and n[0] == 'ite':
        if n[1] == 'true':  return n[2]
        if n[1] == 'false': return n[3]
        if render(n[2]) == render(n[3]): return n[2]
    return n

CT = 'Add_Expr' if ctor == 'Add' else 'Mul_Expr'
parent = [CT, 'f0', 'f1']

def ev_at(term):
    return '(assert %s)' % render(subst(ev_eq, {ev_v[0]: term}))

# T -- the optimizer's result at the parent, simplified through the constructor
# case split. Derived from the optimizer's OWN defining equation, so it works
# whether or not a smart constructor was inlined into it.
T_raw = subst(opt_eq[2], {opt_v[0]: parent})
T = simp(T_raw)

if mode == 'checksimp':
    # SELF-CHECK: the simplifier only applies datatype axioms, so the simplified
    # term must be PROVABLY equal to the raw substituted one. Emits a script whose
    # `unsat` witnesses that; a `sat` would mean the generator rewrote the term
    # into something the datatype theory does not license.
    import subprocess
    chk = "\n".join([dt_line] + declare_funs +
                    ["(declare-const f0 Expr)", "(declare-const f1 Expr)",
                     "(assert (not (= %s %s)))" % (render(T_raw), render(T)),
                     "(check-sat)"])
    open(out_path, 'w').write(chk + "\n")
    r = subprocess.run(["z3", "-smt2", out_path], capture_output=True, text=True)
    print("%-9s %-4s simplifier self-check: %s" % (opt_fn, ctor, r.stdout.strip()))
    raise SystemExit(0)

def leaves(n, acc):
    if isinstance(n, list) and n and n[0] == 'ite':
        leaves(n[2], acc); leaves(n[3], acc)
    else:
        if render(n) not in [render(x) for x in acc]: acc.append(n)
    return acc

prem = [('P1 %s defn @ parent' % opt_fn,
         '(assert %s)' % render(subst(opt_eq, {opt_v[0]: parent}))),
        ('P2 fn_ev defn @ parent', ev_at(parent)),
        ('P3 fn_ev defn @ optimizer result T = %s' % render(T), ev_at(T))]
seen = {render(T)}
for lf in leaves(T, []):
    if render(lf) in seen: continue
    seen.add(render(lf))
    prem.append(('P%d fn_ev defn @ branch %s' % (len(prem) + 1, render(lf)), ev_at(lf)))
if mode == 'ext':
    for v in ('f0', 'f1'):
        prem.append(('P%d fn_ev defn @ (%s %s)' % (len(prem) + 1, opt_fn, v),
                     ev_at([opt_fn, v])))

o = [dt_line] + declare_funs + declare_consts + [a for _, a in prem] + asserts[-3:]
o += ['(check-sat)', '(get-info :rlimit)', '(get-info :reason-unknown)']
open(out_path, 'w').write('\n'.join(o) + '\n')
for lbl, _ in prem: print('   ', lbl)
print("wrote %s  (%d definition instances + 2 IH)" % (out_path, len(prem)))
