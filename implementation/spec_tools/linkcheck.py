#!/usr/bin/env python3
"""linkcheck: check that every relative Markdown link and anchor resolves.

Tooling language: Python 3, standard library only (shared tool implementation/spec_tools).
Source: AGENTS.md#link-checker-linkcheck

Usage (from the repository root):
    python3 implementation/spec_tools/linkcheck.py                 # abstraction/ and AGENTS.md
    python3 implementation/spec_tools/linkcheck.py --root ../main  # another checkout

Exit status: 0 OK, 1 problems reported, 2 usage or I/O error.
"""

import argparse
import os
import re
import sys

from mdlinks import heading_anchors, iter_links

DEFAULT_PATHS = ["abstraction", "AGENTS.md"]
SKIP_PREFIXES = ("http://", "https://", "mailto:")
LINE_FRAGMENT_RE = re.compile(r"L(\d+)")


def markdown_files(root, paths):
    """Expand the given paths (relative to root) into sorted .md files."""
    files = []
    for p in paths:
        full = os.path.join(root, p)
        if os.path.isdir(full):
            for dirpath, dirnames, filenames in os.walk(full):
                dirnames.sort()
                files += [os.path.join(dirpath, f) for f in sorted(filenames) if f.endswith(".md")]
        elif os.path.isfile(full):
            files.append(full)
        else:
            raise FileNotFoundError(p)
    return files


def check(root, paths, out=sys.stdout):
    """Print one line per problem, then OK or PROBLEMS. Returns the exit status (0 or 1)."""
    anchors_cache, lines_cache, problems = {}, {}, 0

    def anchors(path):
        if path not in anchors_cache:
            with open(path, encoding="utf-8") as f:
                anchors_cache[path] = heading_anchors(f.read())
        return anchors_cache[path]

    def line_count(path):
        if path not in lines_cache:
            with open(path, "rb") as f:
                lines_cache[path] = f.read().count(b"\n")
        return lines_cache[path]

    for md in markdown_files(root, paths):
        rel = os.path.relpath(md, root).replace(os.sep, "/")
        with open(md, encoding="utf-8") as f:
            text = f.read()
        for _, target in iter_links(text):
            if target.startswith(SKIP_PREFIXES):
                continue
            path, _, frag = target.partition("#")
            dest = os.path.normpath(os.path.join(os.path.dirname(md), path)) if path else md
            problem = None
            if path and not os.path.exists(dest):
                problem = "MISSING FILE"
            elif frag and dest.endswith(".md"):
                if frag not in anchors(dest):
                    problem = "MISSING ANCHOR"
            elif frag and os.path.isfile(dest):
                m = LINE_FRAGMENT_RE.fullmatch(frag)
                if m and int(m.group(1)) > line_count(dest):
                    problem = "LINE OUT OF RANGE"
            if problem:
                problems += 1
                print(f"{problem} {rel}: {target}", file=out)
    print("PROBLEMS" if problems else "OK", file=out)
    return 1 if problems else 0


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--root", default=".", help="repository root (default: .)")
    p.add_argument("paths", nargs="*", default=DEFAULT_PATHS, help="files or directories, relative to root")
    args = p.parse_args(argv)
    try:
        return check(args.root, args.paths)
    except (OSError, UnicodeDecodeError) as e:
        print(f"error: {e}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
