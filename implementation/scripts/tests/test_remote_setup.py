# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/remote_setup/design.md (remote_setup v0.4.1)
import os
import re
import tempfile
import unittest

from benchrunner import remote_setup
from benchrunner.config import Machine
from benchrunner.transport import CommandResult

GOOD = """lang = "go"
required_tools = ["go"]
build = "go build ./..."
[binaries]
server = "bin/server"
loadgen = "bin/loadgen"
ingress = "bin/ingress"
"""


class ManifestTest(unittest.TestCase):
    def read(self, text, lang="go"):
        with tempfile.NamedTemporaryFile("w", suffix=".toml", delete=False) as f:
            f.write(text)
        try:
            return remote_setup.read_manifest(f.name, lang)
        finally:
            os.unlink(f.name)

    # Design: abstraction/benchmark_runner/remote_setup/design.md#buildmanifest
    def test_good_and_bad(self):
        m = self.read(GOOD)
        self.assertEqual((m.required_tools, m.binaries["server"]), (["go"], "bin/server"))
        for bad, fragment in [(GOOD, "branch says"), (GOOD.replace('"bin/server"', '"../server"'), "without '..'"),
                              (GOOD.replace('["go"]', '["g o"]'), "command names"),
                              ("extra = 1\n" + GOOD, "optionally thread_env"),
                              ('thread_env = "1X"\n' + GOOD, "environment variable name"),
                              (GOOD.replace('ingress = "bin/ingress"', 'canceller = "bin/canceller"'),
                               "exactly server, loadgen, ingress")]:
            lang = "rust" if fragment == "branch says" else "go"
            with self.assertRaises(remote_setup.PinError) as cm:
                self.read(bad, lang)
            self.assertIn(fragment, str(cm.exception))

    # Design: abstraction/benchmark_runner/remote_setup/design.md#buildmanifest
    def test_gc_trace_hook(self):
        m = self.read(GOOD)
        self.assertEqual((m.gc_trace_env, m.gc_trace_regex), (None, None))
        regex = (r"^gc (?P<cycle>\d+) @(?P<start_s>[0-9.]+)s \d+%: (?P<stw_start_ms>[0-9.]+)"
                 r"\+(?P<mark_ms>[0-9.]+)\+(?P<stw_end_ms>[0-9.]+) ms clock")
        good = f"gc_trace_env = [\"GODEBUG=gctrace=1\"]\ngc_trace_regex = '{regex}'\n" + GOOD
        m = self.read(good)
        self.assertEqual((m.gc_trace_env, m.gc_trace_regex), (["GODEBUG=gctrace=1"], regex))
        for bad, why in (('gc_trace_env = ["GODEBUG=gctrace=1"]\n' + GOOD, "together"),
                         ("gc_trace_regex = '^gc'\n" + GOOD, "together"),
                         ('gc_trace_env = ["GODEBUG"]\ngc_trace_regex = \'x\'\n' + GOOD, "NAME=value"),
                         ('gc_trace_env = []\ngc_trace_regex = \'x\'\n' + GOOD, "NAME=value"),
                         ('gc_trace_env = ["A=1"]\ngc_trace_regex = \'(\'\n' + GOOD, "does not compile"),
                         ('gc_trace_env = ["A=1"]\ngc_trace_regex = \'(?P<cycle>x)\'\n' + GOOD, "named groups")):
            with self.assertRaises(remote_setup.PinError) as cm:
                self.read(bad)
            self.assertIn(why, str(cm.exception))
        # the real Go manifest, when this shared tool sits on the impl/go branch
        go_path = os.path.join(os.path.dirname(__file__), "..", "..", "go", "benchmark_manifest.toml")
        if not os.path.exists(go_path):
            return
        go = remote_setup.read_manifest(go_path, "go")
        line = "gc 12 @4.507s 3%: 0.018+5.1+0.042 ms clock, 0.07+1.2/9.8/20+0.17 ms cpu, 3->3->2 MB, 4 MB goal, 0 P"
        mt = re.match(go.gc_trace_regex, line)
        self.assertEqual([mt.group(g) for g in remote_setup.GC_TRACE_GROUPS], ["12", "4.507", "0.018", "5.1", "0.042"])

    # Design: abstraction/benchmark_runner/remote_setup/design.md#buildmanifest
    def test_thread_env(self):
        self.assertIsNone(self.read(GOOD).thread_env)
        self.assertEqual(self.read('thread_env = "GOMAXPROCS"\n' + GOOD).thread_env, "GOMAXPROCS")


class FakeTransport:
    """Every command succeeds, except `command -v taskset` unless has_taskset."""

    def __init__(self, has_taskset, commit):
        self.has_taskset, self.commit = has_taskset, commit

    def resolve(self, path):
        return path.replace("~", "/home/u")

    def run(self, command, timeout_s=120.0):
        if command == "command -v taskset" and not self.has_taskset:
            return CommandResult(1, "", "")
        if command.endswith("rev-parse HEAD"):
            return CommandResult(0, self.commit + "\n", "")
        if command.startswith("test -d"):
            return CommandResult(1, "", "")
        return CommandResult(0, "", "")


class SetupMachineTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/remote_setup/design.md#machine-setup
    def test_cpu_pinning_and_thread_env(self):
        manifest = remote_setup.BuildManifest("go", ["go"], "go build", {p: f"bin/{p}" for p in remote_setup.PROGRAMS},
                                              "GOMAXPROCS")
        pin = remote_setup.SourcePin("u", "impl/go", "go", "c" * 40, manifest)
        machine = Machine("server", "local", None, None, 22, "~/w", "127.0.0.1")
        for has in (True, False):
            ready = remote_setup.setup_machine(FakeTransport(has, "c" * 40), "server", machine, pin, 10)
            self.assertEqual((ready.cpu_pinning, ready.thread_env), (has, "GOMAXPROCS"))


if __name__ == "__main__":
    unittest.main()
