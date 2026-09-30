Central Bazel configuration, macros, toolchains, platforms, and architectural policies.

Bazel is the canonical build and test system for this repository.

Common commands:

bazel build //...

Build all targets.

bazel test //...

Run all tests.

bazel query //...

Inspect the dependency graph.

bazel query 'deps(//services/commerce-api:...)'

Inspect dependencies of a service.
