# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/remote_transport/design.md (remote_transport v0.1.1)
import os
import tempfile
import time
import unittest

from benchrunner import transport


class QuoteTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/remote_transport/design.md#command-quoting
    def test_worked_examples(self):
        cases = {"--global_rate": "--global_rate", "cooperative": "cooperative",
                 "/home/u/cancellation-bench": "/home/u/cancellation-bench",
                 "a b": "'a b'", "it's": "'it'\\''s'", "": "''"}
        for word, want in cases.items():
            self.assertEqual(transport.quote(word), want)


class SshArgvTest(unittest.TestCase):
    # Design: abstraction/benchmark_runner/remote_transport/design.md#run
    def test_command_lines(self):
        t = transport.SshTransport("10.0.0.5", "ubuntu", "/k", 2222)
        self.assertEqual(t.run_argv("echo hi"),
                         ["ssh", "-i", "/k", "-p", "2222", *transport.SSH_OPTIONS, "ubuntu@10.0.0.5", "sh -c 'echo hi'"])
        self.assertEqual(t.copy_argv("/r/f", "/l/f"),
                         ["scp", "-q", "-i", "/k", "-P", "2222", *transport.SSH_OPTIONS, "ubuntu@10.0.0.5:/r/f", "/l/f"])
        self.assertIn("BatchMode=yes", transport.SSH_OPTIONS)


class LocalTransportTest(unittest.TestCase):
    def setUp(self):
        self.t = transport.LocalTransport()
        self.tmp = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.tmp.cleanup()

    # Design: abstraction/benchmark_runner/remote_transport/design.md#run
    def test_run_and_timeout(self):
        r = self.t.run("echo out; echo err >&2; exit 3", 10)
        self.assertEqual((r.exit_status, r.stdout, r.stderr), (3, "out\n", "err\n"))
        self.assertEqual(self.t.run("sleep 5", 0.2).exit_status, -1)
        self.assertEqual(self.t.home(), os.path.expanduser("~"))
        self.assertEqual(self.t.resolve("~/x"), os.path.expanduser("~/x"))

    # Design: abstraction/benchmark_runner/remote_transport/design.md#background-processes
    def test_background_signal_alive(self):
        log = os.path.join(self.tmp.name, "log")
        pid = self.t.start_background("exec sh -c 'echo started; trap \"echo got-int; exit 0\" INT; "
                                      "while :; do sleep 0.05; done'", log)
        time.sleep(0.3)
        self.assertTrue(self.t.alive(pid))
        self.assertTrue(self.t.signal(pid, "INT"))
        for _ in range(50):
            if not self.t.alive(pid):
                break
            time.sleep(0.05)
        self.assertFalse(self.t.alive(pid))
        with open(log) as f:
            self.assertIn("started", f.read())

    # Design: abstraction/benchmark_runner/remote_transport/design.md#fetch
    def test_fetch(self):
        src, dst = os.path.join(self.tmp.name, "a.txt"), os.path.join(self.tmp.name, "sub", "b.txt")
        with open(src, "w") as f:
            f.write("data")
        self.assertTrue(self.t.fetch(src, dst))
        with open(dst) as f:
            self.assertEqual(f.read(), "data")
        self.assertFalse(self.t.fetch(src + ".missing", dst))


if __name__ == "__main__":
    unittest.main()
