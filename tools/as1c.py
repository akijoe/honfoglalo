#!/usr/bin/env python3
"""as1c – a small compiler from ActionScript 1 (a JavaScript subset) to AVM1 bytecode.

patch_swf.py uses it to turn the sources in swf/src/*.as into DoAction tags.

Supported:
  statements   var, function declarations (hoisted), if/else, while, do/while, for(;;), for (k in o),
               switch (=== comparison, fall-through), return, break, continue, blocks
  expressions  number/string/true/false/null/undefined literals, this, arrays, objects, function
               expressions (closures), member access, calls, new, assignment (= += -= ...),
               ++/--, arithmetic, bitwise, comparison, == != === !==, && || ?: , typeof, delete, void
  AS1 actions  trace(x), getURL(url, target), random(n), getTimer(), int(x)

Not supported: try/catch/throw, regular expressions, labels, `in` operator, getters/setters.
Every variable a function uses as a local must be declared with `var` (as in AS1, an undeclared
assignment would go to the timeline).

Usage:  python3 tools/as1c.py file.as   -> prints the size; with -d also the disassembly
"""
import re
import struct
import sys

# ------------------------------------------------------------------------------------------ lexer

KEYWORDS = {'var', 'function', 'if', 'else', 'while', 'do', 'for', 'in', 'return', 'break', 'continue',
            'new', 'this', 'true', 'false', 'null', 'undefined', 'typeof', 'void', 'delete', 'instanceof',
            'switch', 'case', 'default'}
PUNCT = sorted('''>>>= === !== >>> <<= >>= += -= *= /= %= &= |= ^= && || == != <= >= ++ -- << >>
                  { } ( ) [ ] ; , < > + - * / % & | ^ ! ~ ? : = .'''.split(), key=len, reverse=True)
TOKEN_RE = re.compile(r'''
    (?P<ws>\s+|//[^\n]*|/\*.*?\*/)
  | (?P<num>0[xX][0-9a-fA-F]+|(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?)
  | (?P<str>"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')
  | (?P<name>[A-Za-z_$][A-Za-z0-9_$]*)
  | (?P<p>''' + '|'.join(re.escape(p) for p in PUNCT) + r''')
''', re.S | re.X)
ESCAPES = {'n': '\n', 't': '\t', 'r': '\r', 'b': '\b', 'f': '\f', 'v': '\v', '0': '\0'}


class CompileError(Exception):
    pass


def unescape(body):
    out, i = [], 0
    while i < len(body):
        c = body[i]
        if c != '\\':
            out.append(c)
            i += 1
            continue
        n = body[i + 1]
        if n == 'u':
            out.append(chr(int(body[i + 2:i + 6], 16)))
            i += 6
        elif n == 'x':
            out.append(chr(int(body[i + 2:i + 4], 16)))
            i += 4
        else:
            out.append(ESCAPES.get(n, n))
            i += 2
    return ''.join(out)


def tokenize(src, fname):
    toks, pos, line = [], 0, 1
    while pos < len(src):
        m = TOKEN_RE.match(src, pos)
        if not m:
            raise CompileError('%s:%d: unexpected character %r' % (fname, line, src[pos]))
        kind, text = m.lastgroup, m.group()
        if kind == 'num':
            toks.append(('num', int(text, 16) if text[:2] in ('0x', '0X') else
                         (int(text) if re.fullmatch(r'\d+', text) else float(text)), line))
        elif kind == 'str':
            toks.append(('str', unescape(text[1:-1]), line))
        elif kind == 'name':
            toks.append(('kw' if text in KEYWORDS else 'name', text, line))
        elif kind == 'p':
            toks.append(('p', text, line))
        line += text.count('\n')
        pos = m.end()
    toks.append(('eof', None, line))
    return toks

# ------------------------------------------------------------------------------------------ parser

BINARY = [  # lowest precedence first
    ['||'], ['&&'], ['|'], ['^'], ['&'], ['==', '!=', '===', '!=='],
    ['<', '>', '<=', '>=', 'instanceof'], ['<<', '>>', '>>>'], ['+', '-'], ['*', '/', '%'],
]
ASSIGN_OPS = {'=', '+=', '-=', '*=', '/=', '%=', '&=', '|=', '^=', '<<=', '>>=', '>>>='}


class Parser:
    def __init__(self, toks, fname):
        self.toks, self.i, self.fname = toks, 0, fname

    # helpers
    def peek(self, k=0):
        return self.toks[self.i + k]

    def err(self, msg):
        raise CompileError('%s:%d: %s' % (self.fname, self.peek()[2], msg))

    def at(self, kind, val=None):
        t = self.peek()
        return t[0] == kind and (val is None or t[1] == val)

    def at_p(self, val):
        return self.at('p', val)

    def eat(self, kind, val=None):
        if not self.at(kind, val):
            self.err('expected %s, found %r' % (val or kind, self.peek()[1]))
        t = self.toks[self.i]
        self.i += 1
        return t

    def accept(self, kind, val):
        if self.at(kind, val):
            self.i += 1
            return True
        return False

    # statements
    def program(self):
        body = []
        while not self.at('eof'):
            body.append(self.statement())
        return body

    def block(self):
        self.eat('p', '{')
        body = []
        while not self.at_p('}'):
            body.append(self.statement())
        self.eat('p', '}')
        return ('block', body)

    def semi(self):
        self.accept('p', ';')

    def var_decl(self, no_in=False):
        decls = []
        while True:
            name = self.eat('name')[1]
            init = None
            if self.accept('p', '='):
                init = self.assign_expr(no_in)
            decls.append((name, init))
            if not self.accept('p', ','):
                return ('var', decls)

    def statement(self):
        line = self.peek()[2]
        st = self.statement_inner()
        return st + (line,) if st[0] in ('expr',) else st

    def statement_inner(self):
        t = self.peek()
        if t[0] == 'p':
            if t[1] == '{':
                return self.block()
            if t[1] == ';':
                self.i += 1
                return ('block', [])
        if t[0] == 'kw':
            k = t[1]
            if k == 'var':
                self.i += 1
                st = self.var_decl()
                self.semi()
                return st
            if k == 'function' and self.peek(1)[0] == 'name':
                self.i += 1
                name = self.eat('name')[1]
                params, body = self.func_rest()
                return ('funcdecl', name, params, body)
            if k == 'if':
                self.i += 1
                self.eat('p', '(')
                cond = self.expr()
                self.eat('p', ')')
                then = self.statement()
                other = self.statement() if self.accept('kw', 'else') else None
                return ('if', cond, then, other)
            if k == 'while':
                self.i += 1
                self.eat('p', '(')
                cond = self.expr()
                self.eat('p', ')')
                return ('while', cond, self.statement())
            if k == 'do':
                self.i += 1
                body = self.statement()
                self.eat('kw', 'while')
                self.eat('p', '(')
                cond = self.expr()
                self.eat('p', ')')
                self.semi()
                return ('dowhile', body, cond)
            if k == 'for':
                return self.for_stmt()
            if k == 'return':
                self.i += 1
                val = None
                if not (self.at_p(';') or self.at_p('}')):
                    val = self.expr()
                self.semi()
                return ('return', val)
            if k in ('break', 'continue'):
                self.i += 1
                self.semi()
                return (k,)
            if k == 'switch':
                return self.switch_stmt()
        e = self.expr()
        self.semi()
        return ('expr', e)

    def for_stmt(self):
        self.eat('kw', 'for')
        self.eat('p', '(')
        init = None
        if self.at('kw', 'var'):
            self.i += 1
            if self.peek(1)[:2] == ('kw', 'in'):
                name = self.eat('name')[1]
                self.eat('kw', 'in')
                obj = self.expr()
                self.eat('p', ')')
                return ('forin', name, True, obj, self.statement())
            init = self.var_decl(no_in=True)
        elif not self.at_p(';'):
            if self.peek()[0] == 'name' and self.peek(1)[:2] == ('kw', 'in'):
                name = self.eat('name')[1]
                self.eat('kw', 'in')
                obj = self.expr()
                self.eat('p', ')')
                return ('forin', name, False, obj, self.statement())
            init = ('expr', self.expr(), self.peek()[2])
        self.eat('p', ';')
        cond = None if self.at_p(';') else self.expr()
        self.eat('p', ';')
        update = None if self.at_p(')') else self.expr()
        self.eat('p', ')')
        return ('for', init, cond, update, self.statement())

    def switch_stmt(self):
        self.eat('kw', 'switch')
        self.eat('p', '(')
        disc = self.expr()
        self.eat('p', ')')
        self.eat('p', '{')
        cases = []
        while not self.accept('p', '}'):
            if self.accept('kw', 'default'):
                test = None
            else:
                self.eat('kw', 'case')
                test = self.expr()
            self.eat('p', ':')
            body = []
            while not (self.at('kw', 'case') or self.at('kw', 'default') or self.at_p('}')):
                body.append(self.statement())
            cases.append((test, body))
        return ('switch', disc, cases)

    def func_rest(self):
        self.eat('p', '(')
        params = []
        while not self.accept('p', ')'):
            params.append(self.eat('name')[1])
            if not self.at_p(')'):
                self.eat('p', ',')
        return params, self.block()[1]

    # expressions
    def expr(self):
        e = self.assign_expr()
        if self.at_p(','):
            items = [e]
            while self.accept('p', ','):
                items.append(self.assign_expr())
            return ('seq', items)
        return e

    def assign_expr(self, no_in=False):
        left = self.cond_expr(no_in)
        t = self.peek()
        if t[0] == 'p' and t[1] in ASSIGN_OPS:
            if left[0] not in ('name', 'member'):
                self.err('invalid assignment target')
            self.i += 1
            return ('assign', t[1], left, self.assign_expr(no_in))
        return left

    def cond_expr(self, no_in=False):
        c = self.binary(0, no_in)
        if self.accept('p', '?'):
            a = self.assign_expr()
            self.eat('p', ':')
            b = self.assign_expr(no_in)
            return ('cond', c, a, b)
        return c

    def binary(self, level, no_in):
        if level == len(BINARY):
            return self.unary()
        left = self.binary(level + 1, no_in)
        while True:
            t = self.peek()
            if t[0] in ('p', 'kw') and t[1] in BINARY[level]:
                self.i += 1
                right = self.binary(level + 1, no_in)
                left = ('logic' if t[1] in ('&&', '||') else 'bin', t[1], left, right)
            else:
                return left

    def unary(self):
        t = self.peek()
        if (t[0] == 'p' and t[1] in ('!', '-', '+', '~')) or (t[0] == 'kw' and t[1] in ('typeof', 'void', 'delete')):
            self.i += 1
            return ('unary', t[1], self.unary())
        if t[0] == 'p' and t[1] in ('++', '--'):
            self.i += 1
            target = self.unary()
            if target[0] not in ('name', 'member'):
                self.err('invalid ++/-- target')
            return ('update', t[1], True, target)
        e = self.postfix()
        return e

    def postfix(self):
        e = self.call_expr()
        t = self.peek()
        if t[0] == 'p' and t[1] in ('++', '--') and t[2] == self.toks[self.i - 1][2]:
            if e[0] not in ('name', 'member'):
                self.err('invalid ++/-- target')
            self.i += 1
            return ('update', t[1], False, e)
        return e

    def args(self):
        self.eat('p', '(')
        out = []
        while not self.accept('p', ')'):
            out.append(self.assign_expr())
            if not self.at_p(')'):
                self.eat('p', ',')
        return out

    def call_expr(self):
        if self.accept('kw', 'new'):
            callee = self.primary()
            while True:
                if self.accept('p', '.'):
                    callee = ('member', callee, ('str', self.eat_prop()))
                elif self.at_p('['):
                    self.i += 1
                    callee = ('member', callee, self.expr())
                    self.eat('p', ']')
                else:
                    break
            e = ('new', callee, self.args() if self.at_p('(') else [])
        else:
            e = self.primary()
        while True:
            if self.accept('p', '.'):
                e = ('member', e, ('str', self.eat_prop()))
            elif self.at_p('['):
                self.i += 1
                e = ('member', e, self.expr())
                self.eat('p', ']')
            elif self.at_p('('):
                e = ('call', e, self.args())
            else:
                return e

    def eat_prop(self):
        t = self.peek()
        if t[0] in ('name', 'kw'):
            self.i += 1
            return t[1]
        self.err('expected property name')

    def primary(self):
        t = self.peek()
        self.i += 1
        if t[0] == 'num':
            return ('num', t[1])
        if t[0] == 'str':
            return ('str', t[1])
        if t[0] == 'name':
            return ('name', t[1])
        if t[0] == 'kw':
            if t[1] in ('true', 'false'):
                return ('bool', t[1] == 'true')
            if t[1] == 'null':
                return ('null',)
            if t[1] == 'undefined':
                return ('undef',)
            if t[1] == 'this':
                return ('this',)
            if t[1] == 'function':
                name = self.eat('name')[1] if self.at('name') else ''
                params, body = self.func_rest()
                return ('func', name, params, body)
        if t[0] == 'p':
            if t[1] == '(':
                e = self.expr()
                self.eat('p', ')')
                return e
            if t[1] == '[':
                items = []
                while not self.accept('p', ']'):
                    items.append(self.assign_expr())
                    if not self.at_p(']'):
                        self.eat('p', ',')
                return ('arr', items)
            if t[1] == '{':
                props = []
                while not self.accept('p', '}'):
                    k = self.peek()
                    if k[0] not in ('name', 'str', 'num', 'kw'):
                        self.err('expected property name')
                    self.i += 1
                    self.eat('p', ':')
                    props.append((str(k[1]), self.assign_expr()))
                    if not self.at_p('}'):
                        self.eat('p', ',')
                return ('obj', props)
        self.i -= 1
        self.err('unexpected %r' % (t[1],))

# ------------------------------------------------------------------------------------------ codegen

BINOPS = {'+': [0x47], '-': [0x0B], '*': [0x0C], '/': [0x0D], '%': [0x3F], '==': [0x49], '!=': [0x49, 0x12],
          '===': [0x66], '!==': [0x66, 0x12], '<': [0x48], '>': [0x67], '<=': [0x67, 0x12], '>=': [0x48, 0x12],
          '&': [0x60], '|': [0x61], '^': [0x62], '<<': [0x63], '>>': [0x64], '>>>': [0x65], 'instanceof': [0x54]}
UNDEF, NULL = object(), object()


def push_item(v):
    if v is UNDEF:
        return b'\x03'
    if v is NULL:
        return b'\x02'
    if isinstance(v, bool):
        return b'\x05' + (b'\x01' if v else b'\x00')
    if isinstance(v, str):
        b = v.encode('utf-8')
        if b'\0' in b:
            raise CompileError('NUL in string literal')
        return b'\x00' + b + b'\x00'
    if isinstance(v, int) and -2 ** 31 <= v < 2 ** 31:
        return b'\x07' + struct.pack('<i', v)
    d = struct.pack('<d', float(v))
    return b'\x06' + d[4:] + d[:4]          # AVM1 stores doubles as two swapped 32-bit halves


class Code:
    """A flat list of actions with symbolic jumps; adjacent pushes are merged when assembled."""

    def __init__(self):
        self.parts = []
        self.nlabel = 0

    def new_label(self):
        self.nlabel += 1
        return self.nlabel

    def op(self, *codes):
        for c in codes:
            self.parts.append(('op', bytes([c])))

    def raw(self, b):
        self.parts.append(('op', b))

    def push(self, *vals):
        for v in vals:
            self.parts.append(('push', push_item(v)))

    def jump(self, lbl):
        self.parts.append(('jump', lbl))

    def if_(self, lbl):
        self.parts.append(('if', lbl))

    def label(self, lbl):
        self.parts.append(('label', lbl))

    def assemble(self):
        merged = []
        for p in self.parts:
            if p[0] == 'push' and merged and merged[-1][0] == 'push' and len(merged[-1][1]) + len(p[1]) < 0xFFF0:
                merged[-1] = ('push', merged[-1][1] + p[1])
            else:
                merged.append(p)

        def size(p):
            if p[0] == 'label':
                return 0
            if p[0] in ('jump', 'if'):
                return 5
            if p[0] == 'push':
                return 3 + len(p[1])
            return len(p[1])
        pos, where = 0, {}
        for p in merged:
            if p[0] == 'label':
                where[p[1]] = pos
            pos += size(p)
        out, pos = bytearray(), 0
        for p in merged:
            if p[0] in ('jump', 'if'):
                rel = where[p[1]] - (pos + 5)
                if not -32768 <= rel < 32768:
                    raise CompileError('jump too far (split the function)')
                out += (b'\x99' if p[0] == 'jump' else b'\x9d') + struct.pack('<Hh', 2, rel)
            elif p[0] == 'push':
                out += b'\x96' + struct.pack('<H', len(p[1])) + p[1]
            elif p[0] == 'op':
                out += p[1]
            pos += size(p)
        return bytes(out)


class Gen:
    def __init__(self, fname):
        self.fname = fname
        self.c = Code()
        self.loops = []          # (break_label, continue_label or None, is_forin)
        self.tmp = 0
        self.line = 0

    def err(self, msg):
        raise CompileError('%s:%d: %s' % (self.fname, self.line, msg))

    # ---- statements
    def body(self, stmts):
        for st in stmts:                       # hoist function declarations
            if st[0] == 'funcdecl':
                self.c.push(st[1])
                self.function(st[2], st[3])
                self.c.op(0x3C)                # DefineLocal
        for st in stmts:
            if st[0] != 'funcdecl':
                self.stmt(st)

    def stmt(self, st):
        k = st[0]
        c = self.c
        if k == 'expr':
            self.line = st[2]
            self.expr_stmt(st[1])
        elif k == 'var':
            for name, init in st[1]:
                if init is None:
                    c.push(name)
                    c.op(0x41)                 # DefineLocal2
                else:
                    c.push(name)
                    self.expr(init)
                    c.op(0x3C)                 # DefineLocal
        elif k == 'block':
            for s in st[1]:
                if s[0] == 'funcdecl':
                    self.err('function declarations only at the top of a function')
                self.stmt(s)
        elif k == 'funcdecl':
            self.err('function declarations only at the top of a function')
        elif k == 'if':
            l_else, l_end = c.new_label(), c.new_label()
            self.expr(st[1])
            c.op(0x12)
            c.if_(l_else)
            self.stmt(st[2])
            if st[3] is not None:
                c.jump(l_end)
            c.label(l_else)
            if st[3] is not None:
                self.stmt(st[3])
                c.label(l_end)
        elif k == 'while':
            l_top, l_end = c.new_label(), c.new_label()
            c.label(l_top)
            self.expr(st[1])
            c.op(0x12)
            c.if_(l_end)
            self.loop(st[2], l_end, l_top)
            c.jump(l_top)
            c.label(l_end)
        elif k == 'dowhile':
            l_top, l_cont, l_end = c.new_label(), c.new_label(), c.new_label()
            c.label(l_top)
            self.loop(st[1], l_end, l_cont)
            c.label(l_cont)
            self.expr(st[2])
            c.if_(l_top)
            c.label(l_end)
        elif k == 'for':
            _, init, cond, update, body = st
            l_top, l_cont, l_end = c.new_label(), c.new_label(), c.new_label()
            if init is not None:
                self.stmt(init)
            c.label(l_top)
            if cond is not None:
                self.expr(cond)
                c.op(0x12)
                c.if_(l_end)
            self.loop(body, l_end, l_cont)
            c.label(l_cont)
            if update is not None:
                self.expr_stmt(update)
            c.jump(l_top)
            c.label(l_end)
        elif k == 'forin':
            _, name, is_var, obj, body = st
            l_top, l_end, l_brk = c.new_label(), c.new_label(), c.new_label()
            self.expr(obj)
            c.op(0x55)                         # Enumerate2: null, names...
            c.label(l_top)
            c.raw(b'\x87\x01\x00\x00')         # StoreRegister 0
            c.push(NULL)
            c.op(0x49)
            c.if_(l_end)
            c.push(name)
            c.raw(b'\x96\x02\x00\x04\x00')     # push register 0
            c.op(0x3C if is_var else 0x1D)
            self.loop(body, l_brk, l_top, forin=True)
            c.jump(l_top)
            c.label(l_brk)                     # break: drop the remaining names up to the null
            l_drain = c.new_label()
            c.label(l_drain)
            c.raw(b'\x87\x01\x00\x00')
            c.push(NULL)
            c.op(0x49)
            c.op(0x12)
            c.if_(l_drain)
            c.label(l_end)
        elif k == 'return':
            if any(l[2] for l in self.loops):
                self.err('return inside for-in is not supported')
            if st[1] is None:
                c.push(UNDEF)
            else:
                self.expr(st[1])
            c.op(0x3E)
        elif k == 'break':
            if not self.loops:
                self.err('break outside loop')
            c.jump(self.loops[-1][0])
        elif k == 'continue':
            conts = [l for l in self.loops if l[1] is not None]
            if not conts:
                self.err('continue outside loop')
            c.jump(conts[-1][1])
        elif k == 'switch':
            _, disc, cases = st
            self.tmp += 1
            tmp = '$sw%d' % self.tmp
            c.push(tmp)
            self.expr(disc)
            c.op(0x3C)
            l_end = c.new_label()
            labels = [c.new_label() for _ in cases]
            default = None
            for (test, _), lbl in zip(cases, labels):
                if test is None:
                    default = lbl
                    continue
                c.push(tmp)
                c.op(0x1C)
                self.expr(test)
                c.op(0x66)
                c.if_(lbl)
            c.jump(default if default is not None else l_end)
            self.loops.append((l_end, None, False))
            for (_, body), lbl in zip(cases, labels):
                c.label(lbl)
                for s in body:
                    self.stmt(s)
            self.loops.pop()
            c.label(l_end)
        else:
            self.err('unknown statement ' + k)

    def loop(self, body, brk, cont, forin=False):
        self.loops.append((brk, cont, forin))
        self.stmt(body)
        self.loops.pop()

    def expr_stmt(self, e):
        if e[0] == 'assign':
            self.assign(e, False)
        elif e[0] == 'update':
            self.assign(('assign', '+=' if e[1] == '++' else '-=', e[3], ('num', 1)), False)
        elif e[0] == 'call' and e[1] == ('name', 'trace'):
            self.expr(e[2][0] if e[2] else ('str', ''))
            self.c.op(0x26)
        elif e[0] == 'call' and e[1] == ('name', 'getURL'):
            self.expr(e[2][0])
            self.expr(e[2][1] if len(e[2]) > 1 else ('str', ''))
            self.c.raw(b'\x9a\x01\x00\x00')    # GetURL2
        elif e[0] == 'seq':
            for x in e[1]:
                self.expr_stmt(x)
        else:
            self.expr(e)
            self.c.op(0x17)

    # ---- expressions
    def function(self, params, body):
        sub = Gen(self.fname)
        sub.line = self.line
        sub.tmp = self.tmp
        sub.body(body)
        self.tmp = sub.tmp
        code = sub.c.assemble()
        if len(code) > 0xFFFF:
            self.err('function body larger than 64 KB')
        data = b'\x00' + struct.pack('<H', len(params)) + b''.join(p.encode() + b'\x00' for p in params)
        data += struct.pack('<H', len(code))
        self.c.raw(b'\x9b' + struct.pack('<H', len(data)) + data + code)

    def key(self, k):
        self.expr(k)

    def assign(self, e, want):
        _, op, target, value = e
        c = self.c
        if target[0] == 'name':
            name = target[1]
            c.push(name)
            if op == '=':
                self.expr(value)
            else:
                c.push(name)
                c.op(0x1C)
                self.expr(value)
                c.op(*BINOPS[op[:-1]])
            c.op(0x1D)
            if want:
                c.push(name)
                c.op(0x1C)
        else:
            _, obj, k = target
            self.expr(obj)
            self.key(k)
            if op == '=':
                self.expr(value)
            else:
                self.expr(obj)
                self.key(k)
                c.op(0x4E)
                self.expr(value)
                c.op(*BINOPS[op[:-1]])
            c.op(0x4F)
            if want:
                self.expr(obj)
                self.key(k)
                c.op(0x4E)

    def expr(self, e):
        c = self.c
        k = e[0]
        if k == 'num':
            c.push(e[1])
        elif k == 'str':
            c.push(e[1])
        elif k == 'bool':
            c.push(e[1])
        elif k == 'null':
            c.push(NULL)
        elif k == 'undef':
            c.push(UNDEF)
        elif k == 'name':
            c.push(e[1])
            c.op(0x1C)
        elif k == 'this':
            c.push('this')
            c.op(0x1C)
        elif k == 'arr':
            for x in reversed(e[1]):
                self.expr(x)
            c.push(len(e[1]))
            c.op(0x42)
        elif k == 'obj':
            for name, v in e[1]:
                c.push(name)
                self.expr(v)
            c.push(len(e[1]))
            c.op(0x43)
        elif k == 'func':
            self.function(e[2], e[3])
        elif k == 'member':
            self.expr(e[1])
            self.key(e[2])
            c.op(0x4E)
        elif k == 'call':
            callee, args = e[1], e[2]
            if callee[0] == 'name' and callee[1] in ('trace', 'getURL'):
                self.expr_stmt(e)
                c.push(UNDEF)
                return
            if callee[0] == 'name' and callee[1] == 'random' and len(args) == 1:
                self.expr(args[0])
                c.op(0x30)
                return
            if callee[0] == 'name' and callee[1] == 'getTimer' and not args:
                c.op(0x34)
                return
            if callee[0] == 'name' and callee[1] == 'int' and len(args) == 1:
                self.expr(args[0])
                c.op(0x18)
                return
            for a in reversed(args):
                self.expr(a)
            c.push(len(args))
            if callee[0] == 'name':
                c.push(callee[1])
                c.op(0x3D)
            elif callee[0] == 'member':
                self.expr(callee[1])
                self.key(callee[2])
                c.op(0x52)
            else:
                self.expr(callee)
                c.push(UNDEF)
                c.op(0x52)
        elif k == 'new':
            callee, args = e[1], e[2]
            for a in reversed(args):
                self.expr(a)
            c.push(len(args))
            if callee[0] == 'name':
                c.push(callee[1])
                c.op(0x40)
            elif callee[0] == 'member':
                self.expr(callee[1])
                self.key(callee[2])
                c.op(0x53)
            else:
                self.err('unsupported new target')
        elif k == 'unary':
            op, x = e[1], e[2]
            if op == '!':
                self.expr(x)
                c.op(0x12)
            elif op == '-':
                if x[0] == 'num':
                    c.push(-x[1])
                else:
                    c.push(0)
                    self.expr(x)
                    c.op(0x0B)
            elif op == '+':
                self.expr(x)
                c.op(0x4A)
            elif op == '~':
                self.expr(x)
                c.push(-1)
                c.op(0x62)
            elif op == 'typeof':
                self.expr(x)
                c.op(0x44)
            elif op == 'void':
                self.expr(x)
                c.op(0x17)
                c.push(UNDEF)
            elif op == 'delete':
                if x[0] == 'member':
                    self.expr(x[1])
                    self.key(x[2])
                    c.op(0x3A)
                elif x[0] == 'name':
                    c.push(x[1])
                    c.op(0x3B)
                else:
                    self.err('invalid delete')
        elif k == 'update':
            op, prefix, target = e[1], e[2], e[3]
            self.expr(target)                  # old value stays on the stack
            self.assign(('assign', '+=' if op == '++' else '-=', target, ('num', 1)), False)
            if prefix:
                c.op(0x50 if op == '++' else 0x51)
        elif k == 'bin':
            self.expr(e[2])
            self.expr(e[3])
            c.op(*BINOPS[e[1]])
        elif k == 'logic':
            l_end = c.new_label()
            self.expr(e[2])
            c.op(0x4C)                         # Dup
            if e[1] == '&&':
                c.op(0x12)
            c.if_(l_end)
            c.op(0x17)
            self.expr(e[3])
            c.label(l_end)
        elif k == 'cond':
            l_true, l_end = c.new_label(), c.new_label()
            self.expr(e[1])
            c.if_(l_true)
            self.expr(e[3])
            c.jump(l_end)
            c.label(l_true)
            self.expr(e[2])
            c.label(l_end)
        elif k == 'assign':
            self.assign(e, True)
        elif k == 'seq':
            for x in e[1][:-1]:
                self.expr_stmt(x)
            self.expr(e[1][-1])
        else:
            self.err('unknown expression ' + k)


def compile_source(src, fname='<source>'):
    """Compiles a whole file; returns DoAction bytecode (with the End action)."""
    tree = Parser(tokenize(src, fname), fname).program()
    g = Gen(fname)
    g.body(tree)
    return g.c.assemble() + b'\x00'


def compile_file(path):
    with open(path, encoding='utf-8') as f:
        return compile_source(f.read(), path)


if __name__ == '__main__':
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    code = compile_file(sys.argv[-1])
    print('%s: %d bytes' % (sys.argv[-1], len(code)))
    if '-d' in sys.argv:
        import os
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from avm1 import disasm
        lines = []
        disasm(code, lines)
        print('\n'.join(lines))
