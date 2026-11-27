"""Tests for tracegen. Run from the repository root:
    python3 -B -m unittest discover -s implementation/spec_tools/tests -t implementation/spec_tools

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

    def write(self, path, text):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(text)

    def read_design(self):
        with open(self.design) as f:
            return f.read()

    def test_not_traced(self):
        self.write(self.design, DESIGN.replace("<!-- trace:begin -->", "### Not traced\n\n| Design subsection | Reason |\n"
                   "|---|---|\n| [State machine (per `/ping` StreamTask)](#state-machine-per-ping-streamtask) | not realized |\n\n"
                   "<!-- trace:begin -->"))
        self.write(self.code, "// Design: abstraction/x/design.md#requestid\nvar c = 1\n")
        self.assertEqual(self.run_tool(write=True)[0], 0)
        self.assertEqual(self.run_tool(write=False), (0, "OK\n"))
        self.assertIn("| [State machine (per `/ping` StreamTask)](#state-machine-per-ping-streamtask) | not traced |",
                      self.read_design())

    def test_not_traced_problems(self):
        self.write(self.design, DESIGN.replace("<!-- trace:begin -->", "### Not traced\n\n| Design subsection | Reason |\n"
                   "|---|---|\n| [RequestId](#requestid) | x |\n| [Gone](#gone) | x |\n\n<!-- trace:begin -->"))
        status, out = self.run_tool(write=True)
        self.assertEqual(status, 1)
        self.assertIn("LISTED BUT TRACED abstraction/x/design.md#requestid", out)
        self.assertIn("UNKNOWN ANCHOR abstraction/x/design.md#gone", out)

    def test_two_language_sections(self):
        # A Go section and an Envoy section in one file: each run fills only its own table, and the other section's
        # heading is not part of the spec part.
        envoy = "\n## Design specific to Envoy\n\n### Not traced\n\n| Design subsection | Reason |\n|---|---|\n" \
                "| [RequestId](#requestid) | unchanged upstream code: `up.cc` |\n\n<!-- trace:begin -->\n<!-- trace:end -->\n"
        self.write(self.design, DESIGN + envoy)
        self.write(os.path.join(self.root, "implementation", "envoy", "glue.py"),
                   "# Design: abstraction/x/design.md#state-machine-per-ping-streamtask\nx = 1\n")
        self.write(os.path.join(self.root, "implementation", "envoy", "lb.cc"),
                   "// Design: abstraction/x/design.md#state-machine-per-ping-streamtask\nint y;\n")
        self.write(os.path.join(self.root, "implementation", "envoy", "upstream.cc"), "int z;\n")
        self.assertEqual(self.run_tool(write=True)[0], 0)
        out = io.StringIO()
        st = tracegen.run(self.root, "implementation/envoy", "Envoy", [".cc", ".h", ".py:#"], "//", True, out)
        self.assertEqual((st, out.getvalue()), (0, "OK\n"))
        text = self.read_design()
        go, envoy_part = text.split("## Design specific to Envoy")
        self.assertIn("[a.go:5]", go)
        self.assertNotIn("glue.py", go)
        self.assertIn("| [RequestId](#requestid) | not traced |", envoy_part)
        self.assertIn("[glue.py:2](../../implementation/envoy/glue.py#L2), [lb.cc:2](../../implementation/envoy/lb.cc#L2)",
                      envoy_part)
        self.assertNotIn("design-specific-to-go", text)
        out = io.StringIO()
        self.assertEqual(tracegen.run(self.root, "implementation/envoy", "Envoy", [".cc", ".py:#"], "//", False, out), 0)

    def test_parse_exts(self):
        self.assertEqual(tracegen.parse_exts([".cc", ".py:#", ".h:"], "//"), [(".cc", "//"), (".py", "#"), (".h", "//")])


if __name__ == "__main__":
    unittest.main()
