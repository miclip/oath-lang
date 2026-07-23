//! Canonical structures (SPEC §1.2) and the "O1" binary encoder + strict
//! decoder (SPEC §1.1-1.2). No serde: the tag tables, big-endian widths,
//! length-prefixed raw strings, and strict-decoder obligations are implemented
//! by hand so they count as an independent reading. Hash references are held
//! internally as lowercase-hex strings (so elaboration/checking/eval are
//! unchanged); the O1 codec converts to/from the 32 raw bytes on the wire.

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Ty {
    Int,
    Bool,
    // (tag 0x03 reserved — `Str` is now the `Str` datatype, not a primitive type)
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
    // (tag 0x13 reserved — string literals now elaborate to `Str` ctor chains)
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
// O1 binary encoding (SPEC §1.1-1.2)
// ---------------------------------------------------------------------------

const MAGIC: [u8; 2] = [0x4F, 0x31]; // "O1"

fn put_u32(out: &mut Vec<u8>, v: u32) {
    out.extend_from_slice(&v.to_be_bytes());
}

fn put_i64(out: &mut Vec<u8>, v: i64) {
    out.extend_from_slice(&v.to_be_bytes());
}

fn put_str(out: &mut Vec<u8>, s: &str) {
    let b = s.as_bytes();
    put_u32(out, b.len() as u32);
    out.extend_from_slice(b);
}

/// A hash reference on the wire is 32 raw bytes (the referenced def's SHA-256),
/// held internally as 64 lowercase-hex chars.
fn put_hash(out: &mut Vec<u8>, hex: &str) {
    let bytes = hex_decode(hex);
    let mut h = [0u8; 32];
    for (i, b) in bytes.iter().take(32).enumerate() {
        h[i] = *b;
    }
    out.extend_from_slice(&h);
}

fn hex_decode(h: &str) -> Vec<u8> {
    let b = h.as_bytes();
    let mut out = Vec::with_capacity(b.len() / 2);
    let mut i = 0;
    while i + 1 < b.len() {
        let hi = (b[i] as char).to_digit(16).unwrap_or(0);
        let lo = (b[i + 1] as char).to_digit(16).unwrap_or(0);
        out.push(((hi << 4) | lo) as u8);
        i += 2;
    }
    out
}

fn hex_encode(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut s = String::with_capacity(bytes.len() * 2);
    for b in bytes {
        s.push(HEX[(b >> 4) as usize] as char);
        s.push(HEX[(b & 0xf) as usize] as char);
    }
    s
}

fn enc_ty(t: &Ty, out: &mut Vec<u8>) {
    match t {
        Ty::Int => out.push(0x01),
        Ty::Bool => out.push(0x02),
        Ty::Var(i) => {
            out.push(0x04);
            put_u32(out, *i);
        }
        Ty::Fun(a, b) => {
            out.push(0x05);
            enc_ty(a, out);
            enc_ty(b, out);
        }
        Ty::Data { hash, args } => {
            out.push(0x06);
            put_hash(out, hash);
            put_u32(out, args.len() as u32);
            for a in args {
                enc_ty(a, out);
            }
        }
        Ty::Rec { args } => {
            out.push(0x07);
            put_u32(out, args.len() as u32);
            for a in args {
                enc_ty(a, out);
            }
        }
        Ty::Record { names, args } => {
            out.push(0x08);
            put_u32(out, names.len() as u32);
            for (n, a) in names.iter().zip(args.iter()) {
                put_str(out, n);
                enc_ty(a, out);
            }
        }
    }
}

fn enc_term(t: &Term, out: &mut Vec<u8>) {
    match t {
        Term::Var(i) => {
            out.push(0x10);
            put_u32(out, *i);
        }
        Term::Int(n) => {
            out.push(0x11);
            put_i64(out, *n);
        }
        Term::Bool(b) => {
            out.push(0x12);
            out.push(if *b { 0x01 } else { 0x00 });
        }
        Term::Lam { ty, a } => {
            out.push(0x14);
            enc_ty(ty, out);
            enc_term(a, out);
        }
        Term::App { a, b } => {
            out.push(0x15);
            enc_term(a, out);
            enc_term(b, out);
        }
        Term::Let { ty, a, b } => {
            out.push(0x16);
            enc_ty(ty, out);
            enc_term(a, out);
            enc_term(b, out);
        }
        Term::If { a, b, c } => {
            out.push(0x17);
            enc_term(a, out);
            enc_term(b, out);
            enc_term(c, out);
        }
        Term::Prim { op, args } => {
            out.push(0x18);
            put_str(out, op);
            put_u32(out, args.len() as u32);
            for a in args {
                enc_term(a, out);
            }
        }
        Term::Ref { hash, tyargs } => {
            out.push(0x19);
            put_hash(out, hash);
            put_u32(out, tyargs.len() as u32);
            for a in tyargs {
                enc_ty(a, out);
            }
        }
        Term::SelfRef { tyargs } => {
            out.push(0x1A);
            put_u32(out, tyargs.len() as u32);
            for a in tyargs {
                enc_ty(a, out);
            }
        }
        Term::Ctor { hash, idx, tyargs, args } => {
            out.push(0x1B);
            put_hash(out, hash);
            put_u32(out, *idx);
            put_u32(out, tyargs.len() as u32);
            for a in tyargs {
                enc_ty(a, out);
            }
            put_u32(out, args.len() as u32);
            for a in args {
                enc_term(a, out);
            }
        }
        Term::Match { hash, a, arms } => {
            out.push(0x1C);
            put_hash(out, hash);
            enc_term(a, out);
            put_u32(out, arms.len() as u32);
            for arm in arms {
                enc_term(arm, out);
            }
        }
        Term::Record { names, args } => {
            out.push(0x1D);
            put_u32(out, names.len() as u32);
            for (n, a) in names.iter().zip(args.iter()) {
                put_str(out, n);
                enc_term(a, out);
            }
        }
        Term::Field { a, op } => {
            out.push(0x1E);
            enc_term(a, out);
            put_str(out, op);
        }
    }
}

fn enc_def(d: &Def, out: &mut Vec<u8>) {
    match d {
        Def::Data { tyvars, ctors } => {
            out.push(0x01);
            put_u32(out, *tyvars);
            put_u32(out, ctors.len() as u32);
            for fields in ctors {
                put_u32(out, fields.len() as u32);
                for f in fields {
                    enc_ty(f, out);
                }
            }
        }
        Def::Func { tyvars, ty, body, props } => {
            out.push(0x02);
            put_u32(out, *tyvars);
            enc_ty(ty, out);
            enc_term(body, out);
            put_u32(out, props.len() as u32);
            for p in props {
                put_u32(out, p.binders.len() as u32);
                for b in &p.binders {
                    enc_ty(b, out);
                }
                enc_term(&p.body, out);
            }
        }
    }
}

/// The canonical bytes hashed for identity: magic ++ Def (SPEC §1).
pub fn canonical_bytes(d: &Def) -> Vec<u8> {
    let mut out = Vec::new();
    out.extend_from_slice(&MAGIC);
    enc_def(d, &mut out);
    out
}

// ---------------------------------------------------------------------------
// O1 strict decoder (SPEC §1.2): unknown tags, malformed bool bytes, records
// not strictly ascending, truncation, and trailing bytes are all rejected, so
// decode∘encode is the identity and no second encoding of a Def exists.
// ---------------------------------------------------------------------------

struct Cur<'a> {
    b: &'a [u8],
    p: usize,
}

impl<'a> Cur<'a> {
    fn u8(&mut self) -> Result<u8, String> {
        let v = *self.b.get(self.p).ok_or("O1: truncated (u8)")?;
        self.p += 1;
        Ok(v)
    }
    fn u32(&mut self) -> Result<u32, String> {
        if self.p + 4 > self.b.len() {
            return Err("O1: truncated (u32)".into());
        }
        let v = u32::from_be_bytes([self.b[self.p], self.b[self.p + 1], self.b[self.p + 2], self.b[self.p + 3]]);
        self.p += 4;
        Ok(v)
    }
    fn i64(&mut self) -> Result<i64, String> {
        if self.p + 8 > self.b.len() {
            return Err("O1: truncated (i64)".into());
        }
        let mut a = [0u8; 8];
        a.copy_from_slice(&self.b[self.p..self.p + 8]);
        self.p += 8;
        Ok(i64::from_be_bytes(a))
    }
    fn take(&mut self, n: usize) -> Result<&'a [u8], String> {
        if self.p + n > self.b.len() {
            return Err("O1: truncated (bytes)".into());
        }
        let s = &self.b[self.p..self.p + n];
        self.p += n;
        Ok(s)
    }
    fn string(&mut self) -> Result<String, String> {
        let n = self.u32()? as usize;
        let raw = self.take(n)?;
        String::from_utf8(raw.to_vec()).map_err(|_| "O1: invalid UTF-8 in str".into())
    }
    fn hash(&mut self) -> Result<String, String> {
        let raw = self.take(32)?;
        Ok(hex_encode(raw))
    }
    fn boolean(&mut self) -> Result<bool, String> {
        match self.u8()? {
            0x00 => Ok(false),
            0x01 => Ok(true),
            _ => Err("O1: malformed bool byte".into()),
        }
    }
}

fn dec_ty(c: &mut Cur) -> Result<Ty, String> {
    match c.u8()? {
        0x01 => Ok(Ty::Int),
        0x02 => Ok(Ty::Bool),
        // 0x03 is RESERVED (was the `str` primitive type) — strict decoders reject.
        0x04 => Ok(Ty::Var(c.u32()?)),
        0x05 => {
            let a = dec_ty(c)?;
            let b = dec_ty(c)?;
            Ok(Ty::Fun(Box::new(a), Box::new(b)))
        }
        0x06 => {
            let hash = c.hash()?;
            let n = c.u32()? as usize;
            let mut args = Vec::with_capacity(n);
            for _ in 0..n {
                args.push(dec_ty(c)?);
            }
            Ok(Ty::Data { hash, args })
        }
        0x07 => {
            let n = c.u32()? as usize;
            let mut args = Vec::with_capacity(n);
            for _ in 0..n {
                args.push(dec_ty(c)?);
            }
            Ok(Ty::Rec { args })
        }
        0x08 => {
            let n = c.u32()? as usize;
            let mut names = Vec::with_capacity(n);
            let mut args = Vec::with_capacity(n);
            for _ in 0..n {
                names.push(c.string()?);
                args.push(dec_ty(c)?);
            }
            check_ascending(&names)?;
            Ok(Ty::Record { names, args })
        }
        t => Err(format!("O1: unknown Ty tag 0x{:02x}", t)),
    }
}

fn dec_term(c: &mut Cur) -> Result<Term, String> {
    match c.u8()? {
        0x10 => Ok(Term::Var(c.u32()?)),
        0x11 => Ok(Term::Int(c.i64()?)),
        0x12 => Ok(Term::Bool(c.boolean()?)),
        // 0x13 is RESERVED (was the string-literal term) — strict decoders reject.
        0x14 => {
            let ty = dec_ty(c)?;
            let a = dec_term(c)?;
            Ok(Term::Lam { ty, a: Box::new(a) })
        }
        0x15 => {
            let a = dec_term(c)?;
            let b = dec_term(c)?;
            Ok(Term::App { a: Box::new(a), b: Box::new(b) })
        }
        0x16 => {
            let ty = dec_ty(c)?;
            let a = dec_term(c)?;
            let b = dec_term(c)?;
            Ok(Term::Let { ty, a: Box::new(a), b: Box::new(b) })
        }
        0x17 => {
            let a = dec_term(c)?;
            let b = dec_term(c)?;
            let d = dec_term(c)?;
            Ok(Term::If { a: Box::new(a), b: Box::new(b), c: Box::new(d) })
        }
        0x18 => {
            let op = c.string()?;
            let n = c.u32()? as usize;
            let mut args = Vec::with_capacity(n);
            for _ in 0..n {
                args.push(dec_term(c)?);
            }
            Ok(Term::Prim { op, args })
        }
        0x19 => {
            let hash = c.hash()?;
            let n = c.u32()? as usize;
            let mut tyargs = Vec::with_capacity(n);
            for _ in 0..n {
                tyargs.push(dec_ty(c)?);
            }
            Ok(Term::Ref { hash, tyargs })
        }
        0x1A => {
            let n = c.u32()? as usize;
            let mut tyargs = Vec::with_capacity(n);
            for _ in 0..n {
                tyargs.push(dec_ty(c)?);
            }
            Ok(Term::SelfRef { tyargs })
        }
        0x1B => {
            let hash = c.hash()?;
            let idx = c.u32()?;
            let n = c.u32()? as usize;
            let mut tyargs = Vec::with_capacity(n);
            for _ in 0..n {
                tyargs.push(dec_ty(c)?);
            }
            let m = c.u32()? as usize;
            let mut args = Vec::with_capacity(m);
            for _ in 0..m {
                args.push(dec_term(c)?);
            }
            Ok(Term::Ctor { hash, idx, tyargs, args })
        }
        0x1C => {
            let hash = c.hash()?;
            let a = dec_term(c)?;
            let n = c.u32()? as usize;
            let mut arms = Vec::with_capacity(n);
            for _ in 0..n {
                arms.push(dec_term(c)?);
            }
            Ok(Term::Match { hash, a: Box::new(a), arms })
        }
        0x1D => {
            let n = c.u32()? as usize;
            let mut names = Vec::with_capacity(n);
            let mut args = Vec::with_capacity(n);
            for _ in 0..n {
                names.push(c.string()?);
                args.push(dec_term(c)?);
            }
            check_ascending(&names)?;
            Ok(Term::Record { names, args })
        }
        0x1E => {
            let a = dec_term(c)?;
            let op = c.string()?;
            Ok(Term::Field { a: Box::new(a), op })
        }
        t => Err(format!("O1: unknown Term tag 0x{:02x}", t)),
    }
}

fn check_ascending(names: &[String]) -> Result<(), String> {
    for w in names.windows(2) {
        if w[0].as_bytes() >= w[1].as_bytes() {
            return Err("O1: record names not strictly ascending".into());
        }
    }
    Ok(())
}

/// Strict decode of canonical bytes back into a `Def`. Rejects a bad magic,
/// unknown tags, malformed bool bytes, non-ascending record names, truncation,
/// and any trailing bytes.
pub fn decode(bytes: &[u8]) -> Result<Def, String> {
    let mut c = Cur { b: bytes, p: 0 };
    let m = c.take(2)?;
    if m != MAGIC {
        return Err("O1: bad magic".into());
    }
    let def = match c.u8()? {
        0x01 => {
            let tyvars = c.u32()?;
            let nc = c.u32()? as usize;
            let mut ctors = Vec::with_capacity(nc);
            for _ in 0..nc {
                let nf = c.u32()? as usize;
                let mut fields = Vec::with_capacity(nf);
                for _ in 0..nf {
                    fields.push(dec_ty(&mut c)?);
                }
                ctors.push(fields);
            }
            Def::Data { tyvars, ctors }
        }
        0x02 => {
            let tyvars = c.u32()?;
            let ty = dec_ty(&mut c)?;
            let body = dec_term(&mut c)?;
            let np = c.u32()? as usize;
            let mut props = Vec::with_capacity(np);
            for _ in 0..np {
                let nb = c.u32()? as usize;
                let mut binders = Vec::with_capacity(nb);
                for _ in 0..nb {
                    binders.push(dec_ty(&mut c)?);
                }
                let pbody = dec_term(&mut c)?;
                props.push(Prop { binders, body: pbody });
            }
            Def::Func { tyvars, ty, body, props }
        }
        t => return Err(format!("O1: unknown Def tag 0x{:02x}", t)),
    };
    if c.p != bytes.len() {
        return Err("O1: trailing bytes after Def".into());
    }
    Ok(def)
}
