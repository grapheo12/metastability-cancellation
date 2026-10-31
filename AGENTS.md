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
For `impl/<lang>` branches, the `design.md` at each leaf level component will have an added `Design specific to <lang>` section, which is to be filled in before an agent touches the actual code.
The `Design specific to <lang>` section must contain, for every design subsection in that file, a detailed list of links to the implementation files and exact line numbers that realize that subsection.
Conversely, implementation references must not exist only as a file-level comment. Comments inside the code body, adjacent to the relevant declarations, state transitions, algorithms, or tests, must link to the exact `design.md` subsection from which that code is derived.
These links are a bidirectional traceability contract: every future design or implementation change must update both directions and keep file names, subsection anchors, and line numbers consistent. A change is incomplete if it leaves either direction stale.


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
3. Maintain a `version.md` at each level of abstraction that stores a version number and last modified datetime for each change. In any `impl/<lang>` branch, in each code file, along with the source design file, have version number annotation. The next round of implementation effort must make sure that the implementation has the latest spec version.


## Commit policy

The structure of a commit should be:

```[spec/impl] <short heading>

<Detailed changelog>

Co-authored by: <Agent name>
```