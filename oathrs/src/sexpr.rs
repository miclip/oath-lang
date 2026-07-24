//! Surface reader (SPEC §1.4). Produces s-expressions distinguishing the
//! three bracket kinds. Line numbers are best-effort for diagnostics only.

use num_bigint::BigInt;

#[derive(Clone, Debug)]
pub enum Sexpr {
    // `Int` is ℤ — arbitrary precision (SPEC §3).
    Int(BigInt),
    // `Rat` is ℚ — held as an already-reduced numerator/denominator pair
    // (SPEC §1.4: rational literals elaborate to reduced form).
    Rat(BigInt, BigInt),
    Str(String),
    Sym(String),
    List(Vec<Sexpr>),
    Brack(Vec<Sexpr>),
    Brace(Vec<Sexpr>),
}

pub struct Reader<'a> {
    bytes: &'a [u8],
    pos: usize,
    line: usize,
}

impl<'a> Reader<'a> {
    pub fn new(src: &'a str) -> Self {
        Reader { bytes: src.as_bytes(), pos: 0, line: 1 }
    }

    fn peek(&self) -> Option<u8> {
        self.bytes.get(self.pos).copied()
    }

    fn bump(&mut self) -> Option<u8> {
        let b = self.bytes.get(self.pos).copied();
        if let Some(c) = b {
            self.pos += 1;
            if c == b'\n' {
                self.line += 1;
            }
        }
        b
    }

    fn skip_ws(&mut self) {
        while let Some(c) = self.peek() {
            match c {
                b' ' | b'\t' | b'\r' | b'\n' => {
                    self.bump();
                }
                b';' => {
                    while let Some(c) = self.peek() {
                        if c == b'\n' {
                            break;
                        }
                        self.bump();
                    }
                }
                _ => break,
            }
        }
    }

    /// Read all top-level forms.
    pub fn read_all(&mut self) -> Result<Vec<Sexpr>, String> {
        let mut out = Vec::new();
        loop {
            self.skip_ws();
            if self.peek().is_none() {
                break;
            }
            out.push(self.read_form()?);
        }
        Ok(out)
    }

    fn read_form(&mut self) -> Result<Sexpr, String> {
        self.skip_ws();
        let c = match self.peek() {
            Some(c) => c,
            None => return Err(format!("line {}: unexpected end of input", self.line)),
        };
        match c {
            b'(' => self.read_seq(b'(', b')'),
            b'[' => self.read_seq(b'[', b']'),
            b'{' => self.read_seq(b'{', b'}'),
            b')' | b']' | b'}' => {
                Err(format!("line {}: unexpected closing delimiter", self.line))
            }
            b'"' => self.read_string(),
            _ => self.read_atom(),
        }
    }

    fn read_seq(&mut self, open: u8, close: u8) -> Result<Sexpr, String> {
        let start_line = self.line;
        debug_assert_eq!(self.peek(), Some(open));
        self.bump();
        let mut items = Vec::new();
        loop {
            self.skip_ws();
            match self.peek() {
                None => {
                    return Err(format!("line {}: unterminated list opened here", start_line))
                }
                Some(c) if c == close => {
                    self.bump();
                    break;
                }
                Some(c) if c == b')' || c == b']' || c == b'}' => {
                    return Err(format!("line {}: mismatched delimiter", self.line))
                }
                Some(_) => items.push(self.read_form()?),
            }
        }
        Ok(match open {
            b'(' => Sexpr::List(items),
            b'[' => Sexpr::Brack(items),
            _ => Sexpr::Brace(items),
        })
    }

    fn read_string(&mut self) -> Result<Sexpr, String> {
        let start_line = self.line;
        self.bump(); // opening quote
        let mut s = String::new();
        loop {
            let c = match self.bump() {
                Some(c) => c,
                None => {
                    return Err(format!("line {}: unterminated string literal", start_line))
                }
            };
            match c {
                b'"' => break,
                b'\\' => {
                    let e = match self.bump() {
                        Some(e) => e,
                        None => {
                            return Err(format!(
                                "line {}: unterminated string escape",
                                self.line
                            ))
                        }
                    };
                    match e {
                        b'n' => s.push('\n'),
                        b't' => s.push('\t'),
                        b'"' => s.push('"'),
                        b'\\' => s.push('\\'),
                        _ => {
                            return Err(format!(
                                "line {}: invalid string escape \\{}",
                                self.line, e as char
                            ))
                        }
                    }
                }
                _ => {
                    // copy this (possibly multibyte) UTF-8 scalar verbatim
                    let mut buf = vec![c];
                    let cont = utf8_cont_count(c);
                    for _ in 0..cont {
                        match self.bump() {
                            Some(b) => buf.push(b),
                            None => {
                                return Err(format!(
                                    "line {}: truncated UTF-8 in string",
                                    self.line
                                ))
                            }
                        }
                    }
                    match std::str::from_utf8(&buf) {
                        Ok(part) => s.push_str(part),
                        Err(_) => {
                            return Err(format!("line {}: invalid UTF-8 in string", self.line))
                        }
                    }
                }
            }
        }
        Ok(Sexpr::Str(s))
    }

    fn read_atom(&mut self) -> Result<Sexpr, String> {
        let mut buf = Vec::new();
        while let Some(c) = self.peek() {
            match c {
                b' ' | b'\t' | b'\r' | b'\n' | b';' | b'(' | b')' | b'[' | b']' | b'{'
                | b'}' | b'"' => break,
                _ => {
                    buf.push(c);
                    self.bump();
                }
            }
        }
        let tok = String::from_utf8(buf)
            .map_err(|_| format!("line {}: invalid UTF-8 in token", self.line))?;
        // Atom classification (SPEC §1.4), integer FIRST so `3` is an `int` not
        // `3/1`: a decimal `big.Int` token is an `int`; otherwise a token that
        // parses as a rational (`big.Rat` syntax — a decimal like `3.14`/`0.1`,
        // or a fraction `num/den`, either optionally signed) is a `rat`,
        // elaborated to reduced form; otherwise it is a symbol.
        if let Ok(n) = tok.parse::<BigInt>() {
            Ok(Sexpr::Int(n))
        } else if let Some((num, den)) = parse_rational(&tok) {
            Ok(Sexpr::Rat(num, den))
        } else {
            Ok(Sexpr::Sym(tok))
        }
    }
}

/// Parse a `big.Rat`-style rational literal to reduced (numerator, denominator)
/// form (SPEC §1.4). Accepts a fraction `num/den` (both optionally signed
/// integers, denominator nonzero) or a decimal `[sign]int[.frac]` (e.g. `3.14`,
/// `0.1`, `-2.5`). Returns `None` for anything else (which becomes a symbol).
fn parse_rational(tok: &str) -> Option<(BigInt, BigInt)> {
    if tok.is_empty() {
        return None;
    }
    // Fraction form: exactly one '/'.
    if let Some(slash) = tok.find('/') {
        if tok[slash + 1..].contains('/') {
            return None;
        }
        let num = parse_signed_int(&tok[..slash])?;
        let den = parse_signed_int(&tok[slash + 1..])?;
        return crate::ir::reduce_rat(num, den);
    }
    // Decimal form: an optional sign, integer digits, a single '.', frac digits.
    // At least one digit must appear overall, and a '.' must be present (else the
    // integer branch already handled it, or it is a symbol).
    let dot = tok.find('.')?;
    if tok[dot + 1..].contains('.') {
        return None;
    }
    let (sign_neg, rest) = strip_sign(tok);
    let rest_dot = rest.find('.')?;
    let int_part = &rest[..rest_dot];
    let frac_part = &rest[rest_dot + 1..];
    if int_part.is_empty() && frac_part.is_empty() {
        return None;
    }
    if !int_part.bytes().all(|b| b.is_ascii_digit())
        || !frac_part.bytes().all(|b| b.is_ascii_digit())
    {
        return None;
    }
    // value = (int_part ++ frac_part) / 10^len(frac_part)
    let digits = format!("{}{}", int_part, frac_part);
    let mut num: BigInt = if digits.is_empty() {
        BigInt::from(0)
    } else {
        digits.parse::<BigInt>().ok()?
    };
    if sign_neg {
        num = -num;
    }
    let mut den = BigInt::from(1);
    let ten = BigInt::from(10);
    for _ in 0..frac_part.len() {
        den *= &ten;
    }
    crate::ir::reduce_rat(num, den)
}

/// Parse an optionally-signed decimal integer (a fraction component).
fn parse_signed_int(s: &str) -> Option<BigInt> {
    if s.is_empty() {
        return None;
    }
    s.parse::<BigInt>().ok()
}

/// Split a leading `+`/`-` sign, returning (is_negative, remainder).
fn strip_sign(s: &str) -> (bool, &str) {
    if let Some(r) = s.strip_prefix('-') {
        (true, r)
    } else if let Some(r) = s.strip_prefix('+') {
        (false, r)
    } else {
        (false, s)
    }
}

fn utf8_cont_count(lead: u8) -> usize {
    if lead < 0x80 {
        0
    } else if lead >> 5 == 0b110 {
        1
    } else if lead >> 4 == 0b1110 {
        2
    } else if lead >> 3 == 0b11110 {
        3
    } else {
        0
    }
}
