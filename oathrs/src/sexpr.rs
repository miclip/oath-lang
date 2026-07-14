//! Surface reader (SPEC §1.4). Produces s-expressions distinguishing the
//! three bracket kinds. Line numbers are best-effort for diagnostics only.

#[derive(Clone, Debug)]
pub enum Sexpr {
    Int(i64),
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
        // A token that parses as int64 is an integer; otherwise a symbol.
        if let Ok(n) = tok.parse::<i64>() {
            // reject "+5"/leading-zero-ambiguity? Go strconv accepts a leading
            // sign; keep parity with i64 parse which accepts '-'/'+'.
            Ok(Sexpr::Int(n))
        } else {
            Ok(Sexpr::Sym(tok))
        }
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
