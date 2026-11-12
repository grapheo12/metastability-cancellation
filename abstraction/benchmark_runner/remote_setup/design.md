# Remote Setup — Design

## SourcePin

Runs on the operator machine, in the operator's repository.

```
function source_pin(repo_url):
    branch = current branch name                                # detached HEAD -> PinError
    if branch does not start with "impl/": PinError("check out an impl/<lang> branch")
    lang = branch without "impl/"
    if any tracked file is modified or staged: PinError("commit your changes first")   # untracked files are ignored
    commit = full HEAD commit id
    remote = commit id of refs/heads/<branch> at repo_url (ls-remote)
    if remote is missing: PinError("push <branch> to <repo_url> first")
    if remote != commit:  PinError("<repo_url> has <remote> for <branch>, local HEAD is <commit>; push or pull first")
    manifest = read_manifest(implementation/<lang>/benchmark_manifest.toml from the working tree)
    if manifest.lang != lang: PinError("manifest lang mismatch")
    return SourcePin(repo_url, branch, lang, commit, manifest)
```

Reading the manifest from the operator's working tree is safe, because the tree is clean and at `commit`, so the
file is the same one the machines will check out.

## BuildManifest

```
function read_manifest(path):
    doc = parse_toml(path)                       # PinError if missing or malformed
    require the keys lang, required_tools, build, binaries.{server, loadgen, ingress} (exactly these three binaries)
    allow the optional keys thread_env, gc_trace_env, gc_trace_regex; any other key is a PinError
    if thread_env is present: require a non-empty string of letters, digits and "_" not starting with a digit
    if gc_trace_env or gc_trace_regex is present: require both                         # GcTraceHook
        gc_trace_env: a non-empty array of "NAME=value" strings, NAME as for thread_env, value without whitespace
        gc_trace_regex: compiles, and has the named groups cycle, start_s, stw_start_ms, mark_ms, stw_end_ms
    require required_tools to be an array of non-empty strings without whitespace
    require the binaries to be relative paths without ".."
    return BuildManifest          # thread_env, gc_trace_env, gc_trace_regex = none when absent
```

The two `gc_trace_*` keys are the **GcTraceHook**: how to switch on a runtime's per-cycle garbage-collection trace
(the GcTrace) and how to read one of its lines. A language without a collector, or without such a trace, leaves them
out. The Experiment Executor uses them when a config sets `[server] gc_trace`.

The manifest is the language hook of the [Shared tooling](../../../AGENTS.md#shared-tooling) rules. Each
`impl/<lang>` branch provides one, and the runner never assumes anything else about a language.

## Machine setup

```
function setup_machine(t, role, machine, pin, timeout_s):
    workdir  = resolve "~" in machine.workdir with home(t)
    repo_dir = workdir + "/repo"
    check(t, "command -v git", "git is not installed on <role>")
    if run(t, "test -d " + quote(repo_dir + "/.git")).exit_status == 0:
        check(t, "git -C <repo_dir> fetch --quiet <repo_url> <branch>", timeout_s)
    else:
        check(t, "mkdir -p <workdir> && git clone --quiet <repo_url> <repo_dir>", timeout_s)
        check(t, "git -C <repo_dir> fetch --quiet <repo_url> <branch>", timeout_s)
    check(t, "git -C <repo_dir> checkout --quiet --force --detach <commit>")
    require stdout of "git -C <repo_dir> rev-parse HEAD" == commit
    for tool in pin.manifest.required_tools:
        check(t, "command -v <tool>", "<tool> is not installed on <role> (the runner does not install tools)")
    impl_dir = repo_dir + "/implementation/" + pin.lang
    check(t, "cd <impl_dir> && " + pin.manifest.build, timeout_s)     # the build string is used verbatim
    for program in [server, loadgen, ingress]:
        path = impl_dir + "/" + manifest.binaries[program]
        check(t, "test -x " + quote(path), "<program> binary missing after build on <role>")
    cpu_pinning = (run(t, "command -v taskset").exit_status == 0)    # absent is not an error
    return MachineReady(t, role, workdir, repo_dir, binaries, machine.data_addr if role == "server",
                        pin.manifest.thread_env, pin.manifest.gc_trace_env, pin.manifest.gc_trace_regex,
                        cpu_pinning)

function check(t, command, message_or_timeout):
    r = run(t, command, timeout)                  # default timeout 120 s
    if r.exit_status != 0: SetupError("<role>: <message>: <last 20 lines of stderr>")
```

Every word except the manifest's `build` string is passed through `quote`
([../remote_transport/design.md#command-quoting](../remote_transport/design.md#command-quoting)). The `build`
string is a shell command by contract.

Both machines are set up in parallel, and the run continues only if both succeed. If both machines are the same
host with the same `workdir` (e.g. two `local` machines), the two setups would race on one clone. So setup runs
**once** for that pair, and both MachineReady values share the result.
