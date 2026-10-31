# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/remote_setup/design.md (remote_setup v0.1.0)
import os
import tempfile
import unittest

from benchrunner import remote_setup

GOOD = """lang = "go"
required_tools = ["go"]
build = "go build ./..."
[binaries]
server = "bin/server"
loadgen = "bin/loadgen"
canceller = "bin/canceller"
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
                              ("extra = 1\n" + GOOD, "keys must be exactly")]:
            lang = "rust" if fragment == "branch says" else "go"
            with self.assertRaises(remote_setup.PinError) as cm:
                self.read(bad, lang)
            self.assertIn(fragment, str(cm.exception))


if __name__ == "__main__":
    unittest.main()
