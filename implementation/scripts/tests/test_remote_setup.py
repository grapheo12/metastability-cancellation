# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/remote_setup/design.md (remote_setup v0.3.0)
import os
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
