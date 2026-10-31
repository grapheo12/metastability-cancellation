# Remote Setup — Description

Leaf component. It gets both experiment machines ready to run the exact code the operator is looking at:

- **SourcePin** decides *what* to deploy. That is the operator repository's current `impl/<lang>` branch and its
  HEAD commit, which must be committed and already present at `repo_url`.
- The **BuildManifest** is a small declarative file each implementation provides, at
  `implementation/<lang>/benchmark_manifest.toml`. It says which tools the build needs, the build command, and
  where the three binaries end up. It is the only language-specific knowledge the runner uses.
- **Machine setup** runs once per machine, and both machines in parallel. It clones or updates the repository,
  checks out the pinned commit, checks the required tools, builds, and confirms the binaries exist. Tools are only
  checked, never installed.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
