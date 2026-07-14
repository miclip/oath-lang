//! Canonical structures (SPEC §1.2) and the by-hand canonical JSON emitter
//! (SPEC §1.1, §1.5). No serde: the field order, omission and escaping rules
//! are implemented directly so they count as an independent reading.

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Ty {
    Int,
    Bool,
    Str,
    Var(u32),
    Fun(Box<Ty>, Box<Ty>),
    Data { hash: String, args: Vec<Ty> },
    Rec { args: Vec<Ty> },
    // names sorted ascending bytewise, unique; args parallel
    Record { names: Vec<String>, args: Vec<Ty> },
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Term {
    Var(u32),
    Int(i64),
    Bool(bool),
    Str(String),
    Lam { ty: Ty, a: Box<Term> },
    App { a: Box<Term>, b: Box<Term> },
    Let { ty: Ty, a: Box<Term>, b: Box<Term> },
    If { a: Box<Term>, b: Box<Term>, c: Box<Term> },
    Prim { op: String, args: Vec<Term> },
    Ref { hash: String, tyargs: Vec<Ty> },
    SelfRef { tyargs: Vec<Ty> },
    Ctor { hash: String, idx: u32, tyargs: Vec<Ty>, args: Vec<Term> },
    Match { hash: String, a: Box<Term>, arms: Vec<Term> },
    Record { names: Vec<String>, args: Vec<Term> },
    Field { a: Box<Term>, op: String },
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Prop {
    pub binders: Vec<Ty>,
    pub body: Term,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Def {
    Data { tyvars: u32, ctors: Vec<Vec<Ty>> },
    Func { tyvars: u32, ty: Ty, body: Term, props: Vec<Prop> },
}

// ---------------------------------------------------------------------------
// Canonical JSON emission
// ---------------------------------------------------------------------------

const HEX: &[u8; 16] = b"0123456789abcdef";

/// Go encoding/json string encoding with SetEscapeHTML(true), promoted to
/// normative by SPEC §1.1.
pub fn encode_string(s: &str, out: &mut String) {
    out.push('"');
    let bytes = s.as_bytes();
    let mut i = 0usize;
    let mut start = 0usize;
    while i < bytes.len() {
        let b = bytes[i];
        if b < 0x80 {
            let safe =
                b >= 0x20 && b != b'"' && b != b'\\' && b != b'<' && b != b'>' && b != b'&';
            if safe {
                i += 1;
                continue;
            }
            out.push_str(&s[start..i]);
            match b {
                b'"' => out.push_str("\\\""),
                b'\\' => out.push_str("\\\\"),
                b'\n' => out.push_str("\\n"),
                b'\r' => out.push_str("\\r"),
                b'\t' => out.push_str("\\t"),
                _ => {
                    out.push_str("\\u00");
                    out.push(HEX[(b >> 4) as usize] as char);
                    out.push(HEX[(b & 0xf) as usize] as char);
                }
            }
            i += 1;
            start = i;
        } else {
            let ch = s[i..].chars().next().unwrap();
            let len = ch.len_utf8();
            if ch == '\u{2028}' || ch == '\u{2029}' {
                out.push_str(&s[start..i]);
                out.push_str("\\u202");
                out.push(if ch == '\u{2028}' { '8' } else { '9' });
                i += len;
                start = i;
            } else {
                i += len;
            }
        }
    }
    out.push_str(&s[start..]);
    out.push('"');
}

fn key(out: &mut String, first: &mut bool, name: &str) {
    if *first {
        *first = false;
    } else {
        out.push(',');
    }
    out.push('"');
    out.push_str(name);
    out.push_str("\":");
}

fn enc_ty_array(list: &[Ty], out: &mut String) {
    out.push('[');
    for (i, t) in list.iter().enumerate() {
        if i > 0 {
            out.push(',');
        }
        enc_ty(t, out);
    }
    out.push(']');
}

fn enc_str_array(list: &[String], out: &mut String) {
    out.push('[');
    for (i, s) in list.iter().enumerate() {
        if i > 0 {
            out.push(',');
        }
        encode_string(s, out);
    }
    out.push(']');
}

/// Ty field order: k, var, a, b, hash, args, names.
pub fn enc_ty(t: &Ty, out: &mut String) {
    out.push('{');
    let mut first = true;
    match t {
        Ty::Int => {
            key(out, &mut first, "k");
            out.push_str("\"int\"");
        }
        Ty::Bool => {
            key(out, &mut first, "k");
            out.push_str("\"bool\"");
        }
        Ty::Str => {
            key(out, &mut first, "k");
            out.push_str("\"str\"");
        }
        Ty::Var(v) => {
            key(out, &mut first, "k");
            out.push_str("\"var\"");
            if *v != 0 {
                key(out, &mut first, "var");
                out.push_str(&v.to_string());
            }
        }
        Ty::Fun(a, b) => {
            key(out, &mut first, "k");
            out.push_str("\"fun\"");
            key(out, &mut first, "a");
            enc_ty(a, out);
            key(out, &mut first, "b");
            enc_ty(b, out);
        }
        Ty::Data { hash, args } => {
            key(out, &mut first, "k");
            out.push_str("\"data\"");
            if !hash.is_empty() {
                key(out, &mut first, "hash");
                encode_string(hash, out);
            }
            if !args.is_empty() {
                key(out, &mut first, "args");
                enc_ty_array(args, out);
            }
        }
        Ty::Rec { args } => {
            key(out, &mut first, "k");
            out.push_str("\"rec\"");
            if !args.is_empty() {
                key(out, &mut first, "args");
                enc_ty_array(args, out);
            }
        }
        Ty::Record { names, args } => {
            key(out, &mut first, "k");
            out.push_str("\"record\"");
            if !args.is_empty() {
                key(out, &mut first, "args");
                enc_ty_array(args, out);
            }
            if !names.is_empty() {
                key(out, &mut first, "names");
                enc_str_array(names, out);
            }
        }
    }
    out.push('}');
}

fn enc_term_array(list: &[Term], out: &mut String) {
    out.push('[');
    for (i, t) in list.iter().enumerate() {
        if i > 0 {
            out.push(',');
        }
        enc_term(t, out);
    }
    out.push(']');
}

/// Term field order: k, idx, int, bool, str, ty, a, b, c, op, hash, tyargs,
/// args, names, arms.
pub fn enc_term(t: &Term, out: &mut String) {
    out.push('{');
    let mut first = true;
    match t {
        Term::Var(i) => {
            key(out, &mut first, "k");
            out.push_str("\"var\"");
            if *i != 0 {
                key(out, &mut first, "idx");
                out.push_str(&i.to_string());
            }
        }
        Term::Int(n) => {
            key(out, &mut first, "k");
            out.push_str("\"int\"");
            if *n != 0 {
                key(out, &mut first, "int");
                out.push_str(&n.to_string());
            }
        }
        Term::Bool(b) => {
            key(out, &mut first, "k");
            out.push_str("\"bool\"");
            if *b {
                key(out, &mut first, "bool");
                out.push_str("true");
            }
        }
        Term::Str(s) => {
            key(out, &mut first, "k");
            out.push_str("\"str\"");
            if !s.is_empty() {
                key(out, &mut first, "str");
                encode_string(s, out);
            }
        }
        Term::Lam { ty, a } => {
            key(out, &mut first, "k");
            out.push_str("\"lam\"");
            key(out, &mut first, "ty");
            enc_ty(ty, out);
            key(out, &mut first, "a");
            enc_term(a, out);
        }
        Term::App { a, b } => {
            key(out, &mut first, "k");
            out.push_str("\"app\"");
            key(out, &mut first, "a");
            enc_term(a, out);
            key(out, &mut first, "b");
            enc_term(b, out);
        }
        Term::Let { ty, a, b } => {
            key(out, &mut first, "k");
            out.push_str("\"let\"");
            key(out, &mut first, "ty");
            enc_ty(ty, out);
            key(out, &mut first, "a");
            enc_term(a, out);
            key(out, &mut first, "b");
            enc_term(b, out);
        }
        Term::If { a, b, c } => {
            key(out, &mut first, "k");
            out.push_str("\"if\"");
            key(out, &mut first, "a");
            enc_term(a, out);
            key(out, &mut first, "b");
            enc_term(b, out);
            key(out, &mut first, "c");
            enc_term(c, out);
        }
        Term::Prim { op, args } => {
            key(out, &mut first, "k");
            out.push_str("\"prim\"");
            if !op.is_empty() {
                key(out, &mut first, "op");
                encode_string(op, out);
            }
            if !args.is_empty() {
                key(out, &mut first, "args");
                enc_term_array(args, out);
            }
        }
        Term::Ref { hash, tyargs } => {
            key(out, &mut first, "k");
            out.push_str("\"ref\"");
            if !hash.is_empty() {
                key(out, &mut first, "hash");
                encode_string(hash, out);
            }
            if !tyargs.is_empty() {
                key(out, &mut first, "tyargs");
                enc_ty_array(tyargs, out);
            }
        }
        Term::SelfRef { tyargs } => {
            key(out, &mut first, "k");
            out.push_str("\"self\"");
            if !tyargs.is_empty() {
                key(out, &mut first, "tyargs");
                enc_ty_array(tyargs, out);
            }
        }
        Term::Ctor { hash, idx, tyargs, args } => {
            key(out, &mut first, "k");
            out.push_str("\"ctor\"");
            if *idx != 0 {
                key(out, &mut first, "idx");
                out.push_str(&idx.to_string());
            }
            if !hash.is_empty() {
                key(out, &mut first, "hash");
                encode_string(hash, out);
            }
            if !tyargs.is_empty() {
                key(out, &mut first, "tyargs");
                enc_ty_array(tyargs, out);
            }
            if !args.is_empty() {
                key(out, &mut first, "args");
                enc_term_array(args, out);
            }
        }
        Term::Match { hash, a, arms } => {
            key(out, &mut first, "k");
            out.push_str("\"match\"");
            key(out, &mut first, "a");
            enc_term(a, out);
            if !hash.is_empty() {
                key(out, &mut first, "hash");
                encode_string(hash, out);
            }
            if !arms.is_empty() {
                key(out, &mut first, "arms");
                enc_term_array(arms, out);
            }
        }
        Term::Record { names, args } => {
            key(out, &mut first, "k");
            out.push_str("\"record\"");
            if !args.is_empty() {
                key(out, &mut first, "args");
                enc_term_array(args, out);
            }
            if !names.is_empty() {
                key(out, &mut first, "names");
                enc_str_array(names, out);
            }
        }
        Term::Field { a, op } => {
            key(out, &mut first, "k");
            out.push_str("\"field\"");
            key(out, &mut first, "a");
            enc_term(a, out);
            if !op.is_empty() {
                key(out, &mut first, "op");
                encode_string(op, out);
            }
        }
    }
    out.push('}');
}

/// Def field order: k, tyvars, ctors, ty, body, props.
pub fn enc_def(d: &Def, out: &mut String) {
    out.push('{');
    let mut first = true;
    match d {
        Def::Data { tyvars, ctors } => {
            key(out, &mut first, "k");
            out.push_str("\"data\"");
            key(out, &mut first, "tyvars");
            out.push_str(&tyvars.to_string());
            if !ctors.is_empty() {
                key(out, &mut first, "ctors");
                out.push('[');
                for (i, fields) in ctors.iter().enumerate() {
                    if i > 0 {
                        out.push(',');
                    }
                    enc_ty_array(fields, out);
                }
                out.push(']');
            }
        }
        Def::Func { tyvars, ty, body, props } => {
            key(out, &mut first, "k");
            out.push_str("\"func\"");
            key(out, &mut first, "tyvars");
            out.push_str(&tyvars.to_string());
            key(out, &mut first, "ty");
            enc_ty(ty, out);
            key(out, &mut first, "body");
            enc_term(body, out);
            if !props.is_empty() {
                key(out, &mut first, "props");
                out.push('[');
                for (i, p) in props.iter().enumerate() {
                    if i > 0 {
                        out.push(',');
                    }
                    enc_prop(p, out);
                }
                out.push(']');
            }
        }
    }
    out.push('}');
}

/// Prop: binders (always present), body (always present).
fn enc_prop(p: &Prop, out: &mut String) {
    out.push_str("{\"binders\":");
    enc_ty_array(&p.binders, out);
    out.push_str(",\"body\":");
    enc_term(&p.body, out);
    out.push('}');
}

pub fn canonical_bytes(d: &Def) -> String {
    let mut s = String::new();
    enc_def(d, &mut s);
    s
}
