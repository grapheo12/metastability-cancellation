"""mdlinks: Markdown helpers shared by tracegen and linkcheck.

Tooling language: Python 3, standard library only.
Source: AGENTS.md#shared-tooling (spec_tools)
"""

import re

FENCE_RE = re.compile(r"^ {0,3}```")
HEADING_RE = re.compile(r"^#{1,6}\s+(.+?)\s*$")
INLINE_CODE_RE = re.compile(r"(`+).+?\1")
LINK_RE = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")


def slug(heading):
    """GitHub heading anchor (AGENTS.md#algorithm): lowercase, drop everything
    except letters, digits, '_', '-' and ' ', then turn spaces into '-'."""
    s = heading.strip().lower()
    s = "".join(c for c in s if c.isalnum() or c in "_- ")
    return s.replace(" ", "-")


def prose_lines(text):
    """Yield (line number, line) for lines outside fenced code blocks."""
    in_fence = False
    for n, line in enumerate(text.split("\n"), 1):
        if FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if not in_fence:
            yield n, line


def heading_anchors(text):
    """Slugs of all headings (any level) outside fenced code blocks."""
    return {slug(m.group(1)) for _, line in prose_lines(text) for m in [HEADING_RE.match(line)] if m}


def iter_links(text):
    """Yield (line number, target) for inline links outside code blocks and code spans."""
    for n, line in prose_lines(text):
        for m in LINK_RE.finditer(INLINE_CODE_RE.sub("", line)):
            yield n, m.group(1)
