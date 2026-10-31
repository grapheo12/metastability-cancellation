"""Tests for linkcheck and mdlinks. Run from the repository root:
    python3 -B -m unittest discover -s implementation/spec_tools/tests -t implementation/spec_tools

Source: AGENTS.md#link-checker-linkcheck
"""

import io
import os
import tempfile
import unittest

import linkcheck
import mdlinks

DOC = """# Title

## Real Section (with `code`)

[ok file](other.md) [ok anchor](other.md#target) [self](#real-section-with-code)
[bad file](nope.md) [bad anchor](other.md#nope) [web](https://example.com/x)
[line ok](code.go#L2) [line bad](code.go#L9) [code link](code.go#anything)
Inline `[not a link](nope.md)` is ignored.

```
[in fence](nope.md)
## Not a heading
```
"""


class MdlinksTest(unittest.TestCase):
    def test_headings_skip_fences(self):
        anchors = mdlinks.heading_anchors(DOC)
        self.assertIn("real-section-with-code", anchors)
        self.assertNotIn("not-a-heading", anchors)

    def test_links_skip_code(self):
        targets = [t for _, t in mdlinks.iter_links(DOC)]
        self.assertEqual(targets.count("nope.md"), 1)  # the inline-code and fenced ones are skipped


class LinkcheckTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        d = os.path.join(self.tmp.name, "abstraction")
        os.makedirs(d)
        files = {"doc.md": DOC, "other.md": "# Other\n\n## Target\n", "code.go": "a\nb\nc\n"}
        for name, text in files.items():
            with open(os.path.join(d, name), "w") as f:
                f.write(text)
        with open(os.path.join(self.tmp.name, "AGENTS.md"), "w") as f:
            f.write("[ok](abstraction/doc.md)\n")

    def tearDown(self):
        self.tmp.cleanup()

    def test_reports_each_problem_kind(self):
        out = io.StringIO()
        status = linkcheck.check(self.tmp.name, linkcheck.DEFAULT_PATHS, out)
        self.assertEqual(status, 1)
        self.assertEqual(out.getvalue().splitlines(), [
            "MISSING FILE abstraction/doc.md: nope.md",
            "MISSING ANCHOR abstraction/doc.md: other.md#nope",
            "LINE OUT OF RANGE abstraction/doc.md: code.go#L9",
            "PROBLEMS",
        ])

    def test_ok(self):
        out = io.StringIO()
        self.assertEqual(linkcheck.check(self.tmp.name, ["abstraction/other.md", "AGENTS.md"], out), 0)
        self.assertEqual(out.getvalue(), "OK\n")


if __name__ == "__main__":
    unittest.main()
