#!/usr/bin/env python3
"""tracegen: fill and check the traceability tables of every
"Design specific to <Lang>" section from the "Design:" tags in the code.

Source: AGENTS.md#traceability-generator-tracegen

Usage (from the repository root):
    python3 implementation/tracegen/tracegen.py --lang Go --ext .go --comment //          # check
    python3 implementation/tracegen/tracegen.py --lang Go --ext .go --comment // --write  # rewrite, then check

Exit status: 0 OK, 1 problems reported, 2 usage or I/O error.
"""

import argparse
import os
import re
import sys

BEGIN_MARKER = "<!-- trace:begin -->"
END_MARKER = "<!-- trace:end -->"
TAG_RE = re.compile(r"Design:\s*(abstraction/\S+?design\.md)#(\S+)")
HEADING_RE = re.compile(r"^## (.+)$", re.M)


def slug(heading):
    """GitHub heading anchor: lowercase, drop everything except letters,
    digits, '_', '-' and ' ', then turn spaces into '-'."""
    s = heading.strip().lower()
    s = "".join(c for c in s if c.isalnum() or c in "_- ")
    return s.replace(" ", "-")


def collect_tags(root, impl_dir, exts, comment):
    """Map (design path, anchor) -> [(file path from root, line)]. A tag
    resolves to the first following line that is neither blank nor a comment."""
    refs = {}
    for dirpath, dirnames, filenames in os.walk(os.path.join(root, impl_dir)):
        dirnames.sort()
        for name in sorted(filenames):
            if not name.endswith(tuple(exts)):
                continue
            path = os.path.join(dirpath, name)
            rel = os.path.relpath(path, root).replace(os.sep, "/")
            pending = []
            with open(path, encoding="utf-8") as f:
                for n, line in enumerate(f, 1):
                    s = line.strip()
                    is_comment = s.startswith(comment)
                    m = TAG_RE.search(s)
                    if m and is_comment:
                        pending.append(m.groups())
                        continue
                    if pending and s and not is_comment:
                        for k in pending:
                            refs.setdefault(k, []).append((rel, n))
                        pending = []
    return refs


def design_files(root, section_header, refs):
    """Every design.md that has the language section, plus every tagged one."""
    found = {k[0] for k in refs}
    for dirpath, _, filenames in os.walk(os.path.join(root, "abstraction")):
        if "design.md" in filenames:
            path = os.path.join(dirpath, "design.md")
            with open(path, encoding="utf-8") as f:
                if section_header in f.read():
                    found.add(os.path.relpath(path, root).replace(os.sep, "/"))
    return sorted(found)


def build_table(root, design, spec_part, refs, problem):
    """One row per '## ' heading of the spec part, links relative to the design file."""
    to_root = os.path.relpath(root, os.path.dirname(os.path.join(root, design))).replace(os.sep, "/")
    rows = ["| Design subsection | Implementation (file:line) |", "|---|---|"]
    for heading in HEADING_RE.findall(spec_part):
        anchor = slug(heading)
        locs = sorted(set(refs.get((design, anchor), [])))
        if not locs:
            problem(f"UNCOVERED {design}#{anchor}")
        links = ", ".join(f"[{os.path.basename(f)}:{n}]({to_root}/{f}#L{n})" for f, n in locs)
        rows.append(f"| [{heading}](#{anchor}) | {links} |")
    return "\n".join(rows)


def run(root, impl_dir, lang, exts, comment, write, out=sys.stdout):
    """Returns the exit status (0 or 1)."""
    problems = []

    def problem(msg):
        problems.append(msg)
        print(msg, file=out)

    section_header = "## Design specific to " + lang
    refs = collect_tags(root, impl_dir, exts, comment)
    for design in design_files(root, section_header, refs):
        path = os.path.join(root, design)
        if not os.path.exists(path):
            problem(f"MISSING FILE {design}")
            continue
        with open(path, encoding="utf-8") as f:
            text = f.read()
        spec_part = text.split(section_header, 1)[0]
        anchors = {slug(h) for h in HEADING_RE.findall(spec_part)}
        for d, a in sorted(refs):
            if d == design and a not in anchors:
                problem(f"UNKNOWN ANCHOR {design}#{a}")
        table = build_table(root, design, spec_part, refs, problem)
        i, j = text.find(BEGIN_MARKER), text.find(END_MARKER)
        if section_header not in text or i < 0 or j < i:
            problem(f"NO SECTION {design}")
            continue
        updated = text[:i] + BEGIN_MARKER + "\n" + table + "\n" + text[j:]
        if updated == text:
            continue
        if write:
            with open(path, "w", encoding="utf-8") as f:
                f.write(updated)
        else:
            problem(f"STALE {design}")
    print("PROBLEMS" if problems else "OK", file=out)
    return 1 if problems else 0


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--root", default=".", help="repository root (default: .)")
    p.add_argument("--lang", required=True, help="language name in the section header, e.g. Go")
    p.add_argument("--impl", help="implementation directory relative to root (default: implementation/<lang lowercase>)")
    p.add_argument("--ext", action="append", required=True, help="source file extension to scan; repeatable")
    p.add_argument("--comment", required=True, help="line-comment prefix, e.g. // or #")
    p.add_argument("--write", action="store_true", help="rewrite the traceability tables")
    args = p.parse_args(argv)
    impl_dir = args.impl or os.path.join("implementation", args.lang.lower())
    if not os.path.isdir(os.path.join(args.root, impl_dir)):
        print(f"error: no such directory {impl_dir}", file=sys.stderr)
        return 2
    try:
        return run(args.root, impl_dir, args.lang, args.ext, args.comment, args.write)
    except OSError as e:
        print(f"error: {e}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
