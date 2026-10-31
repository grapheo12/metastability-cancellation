"""Tests for tracegen. Run from the repository root:
    python3 -B -m unittest discover -s implementation/tracegen

Source: AGENTS.md#traceability-generator-tracegen
"""

import io
import os
import tempfile
import unittest

import tracegen

DESIGN = """# X — Design

## RequestId

text

## State machine (per `/ping` StreamTask)

text

## Design specific to Go

<!-- trace:begin -->
<!-- trace:end -->
"""

CODE = """package x

// Design: abstraction/x/design.md#requestid
// Design: abstraction/x/design.md#state-machine-per-ping-streamtask
func A() {}

// Design: abstraction/x/design.md#requestid

var b = 1
"""


class SlugTest(unittest.TestCase):
    def test_existing_anchors(self):
        cases = {
            "RequestId": "requestid",
            "Minimum body size": "minimum-body-size",
            "handle_ping": "handle_ping",
            "State machine (per `/ping` StreamTask)": "state-machine-per-ping-streamtask",
            "Status classification (load-generator client)": "status-classification-load-generator-client",
        }
        for heading, want in cases.items():
            self.assertEqual(tracegen.slug(heading), want)


class RunTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name
        os.makedirs(os.path.join(self.root, "abstraction", "x"))
        os.makedirs(os.path.join(self.root, "implementation", "go"))
        self.design = os.path.join(self.root, "abstraction", "x", "design.md")
        self.code = os.path.join(self.root, "implementation", "go", "a.go")
        with open(self.design, "w") as f:
            f.write(DESIGN)
        with open(self.code, "w") as f:
            f.write(CODE)

    def tearDown(self):
        self.tmp.cleanup()

    def run_tool(self, write):
        out = io.StringIO()
        status = tracegen.run(self.root, "implementation/go", "Go", [".go"], "//", write, out)
        return status, out.getvalue()

    def test_write_then_check(self):
        status, out = self.run_tool(write=False)
        self.assertEqual(status, 1)
        self.assertIn("STALE abstraction/x/design.md", out)
        self.assertEqual(self.run_tool(write=True)[0], 0)
        self.assertEqual(self.run_tool(write=False), (0, "OK\n"))
        with open(self.design) as f:
            text = f.read()
        link = "[a.go:5](../../implementation/go/a.go#L5)"
        self.assertIn(f"| [RequestId](#requestid) | {link}, [a.go:9](../../implementation/go/a.go#L9) |", text)
        self.assertIn(f"| [State machine (per `/ping` StreamTask)](#state-machine-per-ping-streamtask) | {link} |", text)

    def test_uncovered_and_unknown(self):
        with open(self.code, "w") as f:
            f.write("// Design: abstraction/x/design.md#nope\nvar c = 1\n")
        status, out = self.run_tool(write=True)
        self.assertEqual(status, 1)
        self.assertIn("UNKNOWN ANCHOR abstraction/x/design.md#nope", out)
        self.assertIn("UNCOVERED abstraction/x/design.md#requestid", out)

    def test_trailing_comment_tag_is_ignored(self):
        with open(self.code, "w") as f:
            f.write("var c = 1 // Design: abstraction/x/design.md#requestid\n")
        _, out = self.run_tool(write=True)
        self.assertIn("UNCOVERED abstraction/x/design.md#requestid", out)


if __name__ == "__main__":
    unittest.main()
