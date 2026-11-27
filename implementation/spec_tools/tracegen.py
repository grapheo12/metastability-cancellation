#!/usr/bin/env python3
"""tracegen: fill and check the traceability tables of every
"Design specific to <Lang>" section from the "Design:" tags in the code.

Tooling language: Python 3, standard library only (shared tool implementation/spec_tools).
Source: AGENTS.md#traceability-generator-tracegen

Usage (from the repository root):
    python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment //          # check
    python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write  # rewrite, then check
An extension may carry its own comment prefix: --ext .cc --ext .h --comment // --ext .py:#

Exit status: 0 OK, 1 problems reported, 2 usage or I/O error.
"""

import argparse
import os
import re
import sys

from mdlinks import slug

BEGIN_MARKER = "<!-- trace:begin -->"
END_MARKER = "<!-- trace:end -->"
TAG_RE = re.compile(r"Design:\s*(abstraction/\S+?design\.md)#(\S+)")
HEADING_RE = re.compile(r"^## (.+)$", re.M)
SECTION_PREFIX = "## Design specific to "
NOT_TRACED = "### Not traced"
LINK_ANCHOR_RE = re.compile(r"^\|\s*\[[^\]]*\]\(#([^)\s]+)\)")


def parse_exts(exts, comment):
    """[(extension, comment prefix)], in command-line order; "ext:prefix" overrides the default prefix."""
    out = []
    for e in exts:
        ext, sep, prefix = e.partition(":")
        out.append((ext, prefix if sep and prefix else comment))
    return out


def collect_tags(root, impl_dir, exts):
    """Map (design path, anchor) -> [(file path from root, line)]. exts is [(extension, comment prefix)]; a file
    uses the prefix of the first extension its name ends with. A tag resolves to the first following line that is
    neither blank nor a comment."""
    refs = {}
    for dirpath, dirnames, filenames in os.walk(os.path.join(root, impl_dir)):
        dirnames.sort()
        for name in sorted(filenames):
            comment = next((c for e, c in exts if name.endswith(e)), None)
            if comment is None:
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


def has_line(text, line):
    return any(l == line for l in text.splitlines())


def design_files(root, section_header, refs):
    """Every design.md that has the language section (a line equal to its header), plus every tagged one."""
    found = {k[0] for k in refs}
    for dirpath, dirnames, filenames in os.walk(os.path.join(root, "abstraction")):
        dirnames[:] = [d for d in dirnames if not d.startswith(".")]  # e.g. proofs/.lake/
        if "design.md" in filenames:
            path = os.path.join(dirpath, "design.md")
            with open(path, encoding="utf-8") as f:
                if has_line(f.read(), section_header):
                    found.add(os.path.relpath(path, root).replace(os.sep, "/"))
    return sorted(found)


def split_design(text, section_header):
    """(spec part, (start, end) of this language's section or None). The spec part ends at the first line that
    starts with SECTION_PREFIX; the section runs from its header line to the next such line, or the end."""
    starts, pos = [], 0
    for line in text.splitlines(keepends=True):
        if line.startswith(SECTION_PREFIX):
            starts.append((pos, line.rstrip("\n")))
        pos += len(line)
    spec_end = starts[0][0] if starts else len(text)
    for i, (start, line) in enumerate(starts):
        if line == section_header:
            end = starts[i + 1][0] if i + 1 < len(starts) else len(text)
            return text[:spec_end], (start, end)
    return text[:spec_end], None


def not_traced_anchors(section):
    """Anchors linked in the first column of the section's '### Not traced' table: the first run of table lines
    after that heading (blank lines before it are skipped; the first other line ends it)."""
    anchors, state = [], "out"  # out -> heading seen -> in table -> done
    for line in section.splitlines():
        s = line.strip()
        if state == "out":
            if s == NOT_TRACED:
                state = "heading"
        elif state == "heading":
            if s.startswith("|"):
                state = "table"
            elif s:
                break  # no table under the heading
        if state == "table":
            if not s.startswith("|"):
                break
            m = LINK_ANCHOR_RE.match(s)
            if m:
                anchors.append(m.group(1))
    return anchors


def build_table(root, design, spec_part, refs, not_traced, problem):
    """One row per '## ' heading of the spec part, links relative to the design file."""
    to_root = os.path.relpath(root, os.path.dirname(os.path.join(root, design))).replace(os.sep, "/")
    rows = ["| Design subsection | Implementation (file:line) |", "|---|---|"]
    for heading in HEADING_RE.findall(spec_part):
        anchor = slug(heading)
        locs = sorted(set(refs.get((design, anchor), [])))
        if not locs and anchor not in not_traced:
            problem(f"UNCOVERED {design}#{anchor}")
        if locs:
            links = ", ".join(f"[{os.path.basename(f)}:{n}]({to_root}/{f}#L{n})" for f, n in locs)
        else:
            links = "not traced" if anchor in not_traced else ""
        rows.append(f"| [{heading}](#{anchor}) | {links} |")
    return "\n".join(rows)


def run(root, impl_dir, lang, exts, comment, write, out=sys.stdout):
    """Returns the exit status (0 or 1)."""
    problems = []

    def problem(msg):
        problems.append(msg)
        print(msg, file=out)

    section_header = SECTION_PREFIX + lang
    refs = collect_tags(root, impl_dir, parse_exts(exts, comment))
    for design in design_files(root, section_header, refs):
        path = os.path.join(root, design)
        if not os.path.exists(path):
            problem(f"MISSING FILE {design}")
            continue
        with open(path, encoding="utf-8") as f:
            text = f.read()
        spec_part, bounds = split_design(text, section_header)
        section = text[bounds[0]:bounds[1]] if bounds else ""
        not_traced = not_traced_anchors(section)
        anchors = {slug(h) for h in HEADING_RE.findall(spec_part)}
        for d, a in sorted(refs):
            if d == design and a not in anchors:
                problem(f"UNKNOWN ANCHOR {design}#{a}")
        for a in not_traced:
            if a not in anchors:
                problem(f"UNKNOWN ANCHOR {design}#{a}")
            elif refs.get((design, a)):
                problem(f"LISTED BUT TRACED {design}#{a}")
        table = build_table(root, design, spec_part, refs, set(not_traced), problem)
        i = section.find(BEGIN_MARKER)
        j = section.find(END_MARKER)
        if not bounds or i < 0 or j < i:
            problem(f"NO SECTION {design}")
            continue
        i, j = bounds[0] + i, bounds[0] + j
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
    p.add_argument("--ext", action="append", required=True,
                   help="source file extension to scan, optionally with its own comment prefix (.py:#); repeatable")
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
