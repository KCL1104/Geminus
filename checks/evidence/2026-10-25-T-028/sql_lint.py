"""A lexical sanity check of T-028's SQL (no PostgreSQL parser is available
here): dollar-quoted bodies are closed, single quotes and parentheses balance
outside comments, strings and dollar quotes, every statement outside a body
ends with ';', and BEGIN is closed by COMMIT or ROLLBACK. It proves no
syntax; the SQL runs only on the coordinator's disposable database.

Usage: sql_lint.py FILE...
"""
import re
import sys

failed = False
for path in sys.argv[1:]:
    text = open(path, encoding="utf-8").read()
    i, n, depth, statements = 0, len(text), 0, 0
    problems = []
    tail = ""  # the current statement outside quotes
    while i < n:
        c = text[i]
        if text.startswith("--", i):
            j = text.find("\n", i)
            i = n if j < 0 else j
            continue
        if c == "\\" and (i == 0 or text[i - 1] == "\n"):  # a psql meta-command line
            j = text.find("\n", i)
            line = text[i:n if j < 0 else j]
            if line.startswith("\\gexec"):
                statements += 1
                if depth:
                    problems.append(f"unbalanced parentheses before {line!r}")
                tail = ""
            i = n if j < 0 else j
            continue
        m = re.compile(r"\$[A-Za-z_]*\$").match(text, i)
        if m:
            tag = m.group(0)
            j = text.find(tag, m.end())
            if j < 0:
                problems.append(f"unclosed {tag} at offset {i}")
                break
            tail += "X"
            i = j + len(tag)
            continue
        if c == "'":
            j = i + 1
            while True:
                j = text.find("'", j)
                if j < 0:
                    problems.append(f"unclosed quote at offset {i}")
                    j = n
                    break
                if text.startswith("''", j):
                    j += 2
                    continue
                break
            tail += "X"
            i = j + 1
            continue
        if c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
            if depth < 0:
                problems.append(f"')' without '(' at offset {i}")
                depth = 0
        elif c == ";":
            if depth:
                problems.append(f"unbalanced parentheses in statement ending at offset {i}")
                depth = 0
            statements += 1
            tail = ""
            i += 1
            continue
        tail += c
        i += 1
    if tail.strip():
        problems.append(f"text after the last statement: {tail.strip()[:60]!r}")
    body = re.sub(r"--[^\n]*", "", text)
    if re.search(r"(?m)^BEGIN;", body) and not re.search(r"(?m)^(COMMIT|ROLLBACK);", body):
        problems.append("BEGIN without COMMIT or ROLLBACK")
    print(f"{path}: {statements} statements, {'OK' if not problems else 'PROBLEMS'}")
    for p in problems:
        print("  " + p)
    failed |= bool(problems)
sys.exit(1 if failed else 0)
