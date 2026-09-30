"""Deprecated macro location.

The macro API that used to live here never worked. It called rules that do not
exist in Bazel's builtin namespace - `native.target`, `native.library`,
`native.label_element`, `native.struct`, `native.execute` and `native.directory`
- so *loading this file failed*, not just calling into it.

The supported macros are in //build:macros.bzl. This file re-exports them so
that a stale `load("//bazel/macros:macros.bzl", ...)` keeps working and points
the author at the real location, instead of failing with a confusing "no such
attribute" error.

Two names are intentionally NOT re-exported, because their old behaviour was
unsafe rather than merely wrong:

  metrics     - was `native.label_element`, which does not exist.
  devtool_lib - was `native.execute`, which does not exist.

Use `metrics_lib` and `devtool_bin` from //build:macros.bzl instead.
"""

load(
    "//build:macros.bzl",
    "alias_target",
    "auth_lib",
    "codegen_lib",
    "config_struct",
    "filegroup_lib",
    "go_bin",
    "go_lib",
    "go_proto",
    "go_proto_lib",
    "go_service",
    "go_test_lib",
    "logging_lib",
    "metrics_lib",
    "platform_visibility",
    "policy_lib",
    "proto_go",
    "proto_lib",
    "secrets_lib",
    "sh_bin",
    "team_boundary",
    "template_dir",
    "tracing_lib",
)

# `native.target`-based macro from the old file: dropped.
platform_security = None

# `native.struct`-based macro from the old file: dropped.
platform_observability = None

templates = template_dir
