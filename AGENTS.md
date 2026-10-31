This is an experiment in writing code completely in Natural Language.
The aim is to have a set of markdown files describing a problem to solve.
And treat them as code. Instead of code in programming languages as code.
We want to treat programming languages as build artifacts and markdown as the source code.

# General principle

We will build a tree of abstractions.
We start from a general description of the problem we are trying to solve.
Then we start by describing the API we expect from our code.
Then a very high level design doc showing modular components.
For each component, recursively, there will be another set of general description, API, design doc.
We keep doing this recursively, until we are discussing exact algorithm to use.
Everything will be in Natural Language and Pseudocode and flowcharts. All files will be markdown.
Nothing implementation language specific will ever be part of the markdown files.
This is a top-down approach of looking at code: Markdowns go from high-level to lower and lower level details.
Human reviewers can review the markdowns in any level of detail they want.


# Repo Organization

The `main` branch of the repo will never have any implementation code.
The agents should only ever output markdown files in appropriate abstraction level, with human feedback.
For a high-level markdown file, it will contain links to lower level files.

There will be different branches for implementations called `impl/<lang>`. For example, a C implementation will be in branch `impl/c`, a Rust implementation in `impl/rust`, etc.
There should never be two language implementations on the same branch. Even when you want to make changes in lang_1 and backport those changes to lang_2. (In such cases: commit and backport the changes as much as possible in `abstraction`, but don't copy back `Design specific to <lang>` sections (see below)).


Stick to this folder structure:

```
<root_project_name>
|- abstraction
   |- description.md
   |- api.md
   |- design.md
   |- <component_1>
      |- description.md
      |- api.md
      |- design.md
      |- <subcomponent_1_1>
         |- ... # A tree of components and subcomponents
      ...
|- implementation
   |- <lang>
      |- <typical folder structure for lang>
```

The `impl/<lang>` branches will have the full structure. `main` should only have `abstraction/`.
For every `implementation/<lang>`, first add the full `<lang>.gitignore` (likely download from Github), before any other file in that directory.
For `impl/<lang>` branches, the `design.md` at each leaf level component will have an added `Design specific to <lang>` section, which is to be filled in before an agent touches the actual code.
The `Design specific to <lang>` section must contain, for every design subsection in that file, a detailed list of links to the implementation files and exact line numbers that realize that subsection.
Conversely, implementation references must not exist only as a file-level comment. Comments inside the code body, adjacent to the relevant declarations, state transitions, algorithms, or tests, must link to the exact `design.md` subsection from which that code is derived.
These links are a bidirectional traceability contract: every future design or implementation change must update both directions and keep file names, subsection anchors, and line numbers consistent. A change is incomplete if it leaves either direction stale.
The code → design direction is written by hand as tags. The design → code direction is generated from those tags by `tracegen` ([Traceability generator (tracegen)](#traceability-generator-tracegen)), never edited by hand.


# Traceability generator (tracegen)

Every `impl/<lang>` branch has its own `tracegen`, written **in that branch's language** and living inside
`implementation/<lang>/` (e.g. `implementation/go/cmd/tracegen`). This section is its spec. An agent starting a
new `impl/<lang>` branch implements `tracegen` from it before writing the `Design specific to <lang>` sections.
The tool is a build tool: it carries no `Design:` tags itself, and its file header cites this section as its source.

## Tags in code

- **File header:** `Source: abstraction/<path>/design.md (<component> v<version>)`, written in a comment. This is the
  version annotation that "Building for future improvements" requires.
- **Body tags:** a comment line containing `Design: abstraction/<path>/design.md#<anchor>`, placed directly above the
  declaration, branch, state transition or test that realizes that subsection. Several tag lines may be stacked.
- A tag **resolves** to the first following line that is neither blank nor a comment. That line number is what gets
  linked. Tags inside trailing comments on code lines are not recognized.
- Optional `API: abstraction/<path>/api.md#<anchor>` comments are allowed for documentation. tracegen ignores them.

## Section format in `design.md`

```
## Design specific to <Lang>

<short header: toolchain, versions, build/test command>

### <Lang> choices
<hand-written: data structures, libraries, deviations in mechanism (never in behavior) and why>

### Traceability
<one sentence naming the tool and the regenerate command>

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [<heading>](#<anchor>) | [<file basename>:<line>](<path from this design.md to repo root>/<file path from root>#L<line>), ... |
<!-- trace:end -->
```

There is one row per `## ` heading that appears **before** `## Design specific to <Lang>`, in document order. Links in
a row are sorted by (file path, line) with duplicates removed.

## Algorithm

```
inputs: repo root, impl dir (implementation/<lang>), Lang, source extension, line-comment prefix, mode (check | write)

refs = map (design path, anchor) -> list of (file path from root, line)
for each source file under impl dir with the extension:
    pending = []
    for each line n (1-based), s = line with surrounding whitespace removed:
        if s starts with the comment prefix and contains a Design tag:
            pending.append((design path, anchor)); continue
        if pending not empty and s is not empty and s does not start with the comment prefix:
            for k in pending: refs[k].append((file, n))
            pending = []

designs = every design.md under abstraction/ that contains "## Design specific to <Lang>",
          plus every design path named by a tag
for each design in sorted(designs):
    spec_part = text before "## Design specific to <Lang>"
    headings  = the "## " headings of spec_part, in order
    report UNKNOWN ANCHOR for each tagged anchor of this design that no heading has
    report UNCOVERED for each heading with no refs
    table = rows as in Section format
    if the section or either marker is missing: report NO SECTION; continue
    updated = text with everything between the markers replaced by table
    if updated != text: in write mode save it, in check mode report STALE
print OK, or PROBLEMS and exit non-zero if anything was reported
```

**Anchor slug** (GitHub rules): lowercase the heading, delete every character that is not a letter, a digit, `_`, `-`
or a space, then replace each space with `-`. For example, ``State machine (per `/ping` StreamTask)`` becomes
`state-machine-per-ping-streamtask`.

The output must be deterministic: the same tree always gives byte-identical tables.

## When to run

- Run `tracegen` in write mode after every implementation change, then in check mode. Check mode must report `OK`
  before committing.
- A spec change that adds, renames or removes a design subsection makes check mode fail on the impl branches
  (UNCOVERED or UNKNOWN ANCHOR) until the code tags follow. That is the intended signal that the implementation is
  behind the spec.


# Spec writing style

Every `description.md`, `api.md`, `design.md` and `version.md` follows the rules below.
`abstraction/` on `main` in this repo is a reference example: compare against it when unsure.

## Common rules (all spec files)

- **Named concepts.** Give every concept one CamelCase name (e.g. `RequestId`, `DrainPhase`).
  Put it in **bold** where a description first introduces it. Use the exact same name in every file at every level.
  List it in the glossary of the root `description.md` as a table: Concept | Meaning | Defined in (a link to the design anchor).
  Consistent names are what make fuzzy-contract search work.
- **Stable anchors.** Each design subsection heading is either the concept's name or a short fixed phrase, so its anchor never changes.
  Cross-references always use `path/file.md#anchor`. A broken link is a bug.
- **Navigation.** Every `description.md` links to its parent `design.md` and to its own `api.md` and `design.md`.
  Every `api.md` links the design anchor of each item it declares. Every non-leaf file links to each child.
- **Units and types.** Names carry units (`_ms`, `_us`, `_ns`, `_s`, `_bytes`), or a table column states the unit.
  Types are abstract: `u32`, `u64`, `float`, `bytes`, `string`, `atomic boolean`, `FIFO`, `map`. Never use language types or library names.
- **Explicit over implied.** Write down defaults, valid ranges, validation errors, exit status, error handling, features that are reserved or not yet used (e.g. "reserved, never sent in v1"), and non-goals.
- **Tables for anything enumerable.** Parameters, constants, record fields, outcomes, status codes, and behavior per mode × situation.

## `description.md`

- Says *what* the component is and *why* it exists, never *how*.
- Opens with "Leaf component." or with the names of its subcomponents.
- Lists responsibilities as a short bullet list using the bold concept names.
- At the root only, has these sections: Problem, a one-paragraph summary of the system, Goals, Non-goals, Glossary.
- Contains no pseudocode and no field lists.

## `api.md`

Sections, in this order, leaving out any that are empty:

1. **Parameters / Constants:** a table of Parameter (or Name) | Type / unit / constraint | Default (or Value) | Meaning.
   A child that restates its parent's parameters must match them exactly and link back to the parent.
2. **Types / records:** a table of Field | Type | Meaning, one per record.
3. **Functions:** each one is a `###` heading holding its full typed signature, including alternative or error returns, e.g.
   `### parse_body(body: bytes) -> u64 | ParseError`.
   Under it go one or two lines of contract (preconditions, guarantees, who calls it) and a link to the design anchor that implements it.
4. **Outputs / wire-visible behavior:** exact formats (column order, JSON shape, headers, status codes).
5. **Lifecycle / exit status:** start, run, stop and error behavior.
6. **Uses:** links to the `api.md` of each dependency, naming the items used.
7. **Subcomponents** (non-leaf only): a table linking each child's `api.md`.

## `design.md`

**Non-leaf:**
- A mermaid diagram of the structure.
- A table of Subcomponent | Responsibility | Spec link.
- The cross-cutting rules every child must share: time base, concurrency model, phases, ordering.
- A short "Why …" paragraph for each structural decision that is not obvious.

**Leaf.** Write these so that a small model produces the same code every time:
- One `##` section per concept or algorithm, containing **exact pseudocode**. State the arithmetic semantics
  (e.g. "unsigned 64-bit, wrapping"), the rounding rule, and use integer arithmetic wherever a result must be exact.
  Name every constant.
- **Worked examples** as tables with a column that checks the arithmetic. Include the boundary cases (minimum, maximum, empty).
- **Races and ties resolved explicitly**, e.g. "whichever is observed first wins; the other is ignored".
- **Ordering requirements** that affect reproducibility (e.g. the order of random draws) get their own subsection.
- **State machines** as mermaid `stateDiagram`. Behavior that differs by mode × situation goes in a table.
- **Concurrency requirements** stated without naming a language, e.g. "X must keep making progress while Y runs.
  If both share one thread, Y must be moved to its own thread."
- **Failure and edge cases** listed: connection errors, malformed input, overflow, shutdown in the middle of work.

## `version.md`

A table of Version | Last modified (UTC, ISO-8601) | Summary, with the newest row last.

## Spec self-check before committing

- Every relative link and anchor resolves (check it with a script, not by eye).
- Names, units, defaults and constraints are the same from parent to child and from `api.md` to `design.md`.
- Every `api.md` item has a design anchor. Every child's `api.md` covers what its parent's `design.md` asks of it.
- Every worked example has been recomputed.


# Coding principle

## Fuzzy contracts

In the old world of writing code, contracts between libraries and software in general was quite strict.
Which leads to a lot of code duplication when agents write code.
If a library doesn't exactly do what we need, the agents will create a sloppy version of the library internally in the codebase to make it do what we need to do.
We absolutely want to stop this. So I introduce this concept of fuzzy contracts here:
Write the markdowns in such a way that it is easy for agents to search for concepts (rather than code snippets) at any level of abstraction.
Imagine a bunch of such repositories being hosted and indexed by a version control system host.
If an agent working on some other project wants to use a project written in this fashion, it creates a fork of the repo, makes changes to the fork in a way that the markdown files `abstraction` are kept consistent (very important!) with the implementation code, and then adds the fork as a submodule to the main code (the submodule should at least contain the `abstraction` part of the repo).
Instead of copying code that it has memorized in training, an agent will potentially use an MCP connected to a RAG search pipeline to search for the repo with the closest possible `abstraction` and go from there.


## Building for fuzzy contracts

So the guiding principle for coding with fuzzy contracts:
1. Have names for concepts that you are building for.
2. Make the abstractions easily searchable for both agents and humans.
3. Cleanly separate out the levels of abstraction, so that modifications are modular.
4. ALWAYS maintain self-consistency: between design and api.md, between components and subcomponents, and between `abstraction` and `impl`s.


## Building for deterministic implementations

In `impl/` branches, an agent (possibly a cheaper model) will write the code following the `abstraction`.
Try best to not touch `abstraction` in impl branches, but if you do, backport it to main, except for the `Design specific to <lang>` section. 

Since we are treating code as a build-artifacts, LLMs are the defacto compiler from natural language to code.
Since LLM output is non-deterministic, we want to be as verbose in our leaf-level `design.md`s as possible, so that even small to medium language models will output same/similar code across implementation generations.
Essentially, each level of abstraction will take away some ambiguity.

For example, an application to build a matchmaking portal might have:
1. At the highest level, a "Preference server" and "Notification Queue".
2. In the next level, in the preference server, it might have a component like "Matching engine" whose design will discuss how it feeds data from another "Preference submission engine" and (abstractly) calculates the best possible matches.
3. In the matching engine, it will have something like: "Use Kuhn/Munkres algorithm to compute matches. The pseudocode of the algorithm is as follows: ...". 
4. In an impl branch, it will have a list of data structure and libraries specific to that language to use.

The right level of abstraction for components and subcomponents should come from human feedback.


## Building for future improvements

Agents are notorious for getting lost in the details to suggest improvements. So high level and low level abstraction and implementations need to be improved separately.
Follow this guideline religiously:
1. In one commit, only an improvement for the spec or the implementation will happen, but not both together.
2. Agentic cycles for optimizing and improving spec/implementation, within one commit, will scope the reasoning in one of three ways:
    - Either only think about the spec at a given level.
    - Or think about improving a subcomponent, then the reasoning should stay limited to that subcomponent.
    - Or improvement is jointly between a subcomponent or its parent. Improvement plans that change two sibling subcomponents is disallowed.
    - Same goes for implementation and leaf-level `design.md`s.
3. Maintain a `version.md` at each level of abstraction that stores a version number and last modified datetime for each change. A version update in a subcomponent updates the version number in the parent as well. In any `impl/<lang>` branch, in each code file, along with the source design file, have version number annotation. The next round of implementation effort must make sure that the implementation has the latest spec version.


## Commit policy

The structure of a commit should be:

```[spec/impl] <short heading>

<Detailed changelog>

Co-authored by: <Agent name>
```

Any changes to AGENTS.md will be done on a separate commit and will be backported to main and all other branches.
