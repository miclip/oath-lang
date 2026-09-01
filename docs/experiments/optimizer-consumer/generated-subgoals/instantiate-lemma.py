"""Build a quantifier-free instantiated script from a prover-emitted induction script.

Every ground equality is produced by SUBSTITUTION into the emitted quantified
equation -- the bodies are never retyped, so the instances cannot drift from the
source axioms.
"""
import sys, re

def tokenize(s):
    return re.findall(r'\(|\)|[^\s()]+', s)

def parse(toks, i=0):
    if toks[i] == '(':
        out, i = [], i + 1
        while toks[i] != ')':
            node, i = parse(toks, i)
            out.append(node)
        return out, i + 1
    return toks[i], i + 1

def render(n):
    if isinstance(n, str):
        return n
    return '(' + ' '.join(render(x) for x in n) + ')'

def subst(n, env):
    if isinstance(n, str):
        return env.get(n, n)
    return [subst(x, env) for x in n]

src, ctor, out_path = sys.argv[1], sys.argv[2], sys.argv[3]
lines = open(src).read().splitlines()

def body_of_forall(line):
    """Return (vars, equation-sexpr) for `(assert (forall (binders) EQ))`,
    unwrapping an `(! EQ :pattern ...)` annotation if present."""
    tree, _ = parse(tokenize(line))
    assert tree[0] == 'assert', tree[0]
    forall = tree[1]
    assert forall[0] == 'forall', forall[0]
    vs = [b[0] for b in forall[1]]
    eq = forall[2]
    if isinstance(eq, list) and eq[0] == '!':
        eq = eq[1]
    return vs, eq

ev_vars,   ev_eq   = body_of_forall(lines[3])   # fn_ev defining equation
opt_vars,  opt_eq  = body_of_forall(lines[4])   # fn_opt defining equation
muls_vars, muls_eq = body_of_forall(lines[5])   # mul-s.sound
adds_vars, adds_eq = body_of_forall(lines[6])   # add-s.sound

CT = 'Add_Expr' if ctor == 'Add' else 'Mul_Expr'
parent = ['%s' % CT, 'f0', 'f1']
lemma_vars, lemma_eq, lemma_name = (
    (adds_vars, adds_eq, 'add-s.sound') if ctor == 'Add'
    else (muls_vars, muls_eq, 'mul-s.sound'))

# Ground instances, each by substitution into the emitted equation above.
i_opt   = subst(opt_eq, {opt_vars[0]: parent})
i_ev    = subst(ev_eq,  {ev_vars[0]:  parent})
i_lemma = subst(lemma_eq, {lemma_vars[0]: ['fn_opt', 'f0'],
                           lemma_vars[1]: ['fn_opt', 'f1']})

o = []
o.append(lines[0])                      # datatype declaration, verbatim
o.append(lines[1]); o.append(lines[2])  # uninterpreted fn_ev / fn_opt, verbatim
o.append(lines[7]); o.append(lines[8]); o.append(lines[9])   # b0, f0, f1
o.append('(assert %s)' % render(i_opt))
o.append('(assert %s)' % render(i_ev))
o.append('(assert %s)' % render(i_lemma))
o.append(lines[10]); o.append(lines[11])   # IH on f0, f1, verbatim
o.append(lines[12])                        # negated goal, verbatim
o.append('(check-sat)')
o.append('(get-info :rlimit)')
o.append('(get-info :reason-unknown)')
open(out_path, 'w').write('\n'.join(o) + '\n')
print("wrote %s (%s, lemma=%s)" % (out_path, ctor, lemma_name))
