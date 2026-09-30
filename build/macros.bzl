"""Reusable build macros for the Ecommerce monorepo.

Every macro here expands to a *real* Bazel rule (`go_library`, `proto_library`,
`go_proto_library`, ...) rather than to a `filegroup`. A filegroup cannot be
compiled against, which is why `//libs/go/errors` used to be unusable as a
dependency of anything.

Import path convention
----------------------
`importpath` is **required** by `go_lib`, `go_bin` and `proto_go`. A Starlark
macro cannot discover the package it is being expanded in, so there is no
sensible default: `github.com/ecommerce/<target name>` is wrong for every
target that does not live in the repository root. Pass the real path, or let
`bazel run //:gazelle` fill it in.

Which macro to use, and what Gazelle will do to it
-------------------------------------------------
Gazelle cannot see through the macros in this file, so whether one is safe to
use depends on how Gazelle is configured for the package that calls it. Two
facts about Gazelle 0.37 drive everything:

1. `config.KindMap` is keyed by the *builtin* kind, so `# gazelle:map_kind` can
   name exactly one macro per builtin. Gazelle can therefore write and index
   `go_lib`, `go_bin` and `go_test_lib` (the three mapped in //BUILD.bazel) and
   nothing else. An unindexable library is dangerous, not merely unsupported: in
   a package Gazelle manages, a missing index makes it silently *delete* the
   matching `deps` entry and then shell out to `go`, which needs the network.
2. Gazelle's model for a `package main` directory is a `go_binary` that *embeds*
   a `go_library` holding the sources (`generateLib`/`isCommand` in
   gazelle/language/go/generate.go). It will split a single hand-written binary
   into two targets, which one Bazel package may not do with the same Go import
   path.

The practical rules:

  * `go_lib`, `go_bin`, `go_test_lib` are safe anywhere. These are the three
    Gazelle is configured for, and //libs/go and //services/commerce/internal
    rely on that.
  * `package main` targets - `go_service`, `devtool_bin`, plain `go_bin` over a
    `package main` directory - belong in a package marked `# gazelle:ignore`.
  * The capability macros below (`logging_lib`, `metrics_lib`, ...) add nothing
    to the target except `tags`, and every check in
    //bazel/aspects:aspects.bzl keys on those tags rather than on the rule kind.
    So //platform/* spells the tags out on a `go_lib` and gets an identical
    target that Gazelle can still index. Prefer that over the capability macros
    in any package Gazelle is allowed to touch.
  * `proto_lib` and `proto_go` belong in `# gazelle:ignore`d packages, because
    `# gazelle:proto disable` stops Gazelle modelling the .proto layout but not
    from editing the file's `load()` statements.

If you are unsure whether a change survives `bazel run //:gazelle`, the check is
one command, and CI runs it:

    bazel run //:gazelle && git diff --exit-code
"""

load("@rules_go//go:def.bzl", "go_binary", "go_library", "go_test")

# `go_proto_library` is NOT exported from @rules_go//go:def.bzl. It lives in
# @rules_go//proto:def.bzl, and loading it from the wrong file leaves the
# generated library's `srcs` pointing at the proto_library label, which fails
# analysis with "does not produce any go_library srcs files".
load("@rules_go//proto:def.bzl", "go_proto_library")
load("@rules_proto//proto:defs.bzl", "proto_library")
load("@rules_shell//shell:sh_binary.bzl", "sh_binary")
load("@rules_shell//shell:sh_library.bzl", "sh_library")
load("@rules_shell//shell:sh_test.bzl", "sh_test")


# Tags applied to every target produced here, so that
# `bazel query 'attr(tags, "language=go", //...)'` is a reliable language filter.
_GO_TAGS = ["language=go"]
_PROTO_TAGS = ["language=proto"]
_SHELL_TAGS = ["language=shell"]

# Applied by every platform-capability macro. The tag is what
# //bazel/aspects:aspects.bzl%platform_api_aspect keys on, and it is set here
# rather than at each call site so that a new capability cannot be added without
# the "must be consumable by every team" policy silently switching itself off.
_LAYER_PLATFORM = "layer=platform"

# -----------------------------------------------------------------------------
# Visibility helpers
# -----------------------------------------------------------------------------

def _public_visibility():
    """Readable by every team. Reserve for platform capabilities and shared libs."""
    return ["//teams:public"]

def _team_visibility(team):
    """Readable only by `team` (and the platform team, which sees everything)."""
    return ["//teams:" + team]

def _platform_visibility():
    return ["//teams:platform"]

def _resolve_visibility(visibility, default):
    return default if visibility == None else visibility

def _require_importpath(name, importpath):
    if not importpath:
        fail(("%s: `importpath` is required. It must be the full Go import path " +
              "for this package, e.g. \"github.com/ecommerce/%s\". A Starlark " +
              "macro cannot infer it.") % (name, name))

# -----------------------------------------------------------------------------
# Go
# -----------------------------------------------------------------------------

def go_lib(
        name,
        srcs,
        importpath,
        deps = [],
        embed = [],
        visibility = None,
        tags = [],
        testonly = False,
        **kwargs):
    """A `go_library` with this repository's conventions applied.

    Gazelle-safe: `go_lib` is one of the three kinds mapped in //BUILD.bazel, so
    Gazelle both writes this name and can index a rule that already uses it.

    Args:
      name: target name.
      srcs: Go source files. Required; use `glob(["**/*.go"], exclude=[...])`
        for a package that owns a directory.
      importpath: full Go import path. Required.
      deps: direct dependencies.
      embed: embedded libraries whose sources this one also compiles.
      visibility: defaults to public.
      testonly: mark the target test-only.
      **kwargs: forwarded to `go_library`.
    """
    _require_importpath(name, importpath)
    go_library(
        name = name,
        srcs = srcs,
        importpath = importpath,
        deps = deps,
        embed = embed,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = _GO_TAGS + tags,
        testonly = testonly,
        **kwargs
    )

def go_bin(
        name,
        srcs,
        importpath,
        deps = [],
        embed = [],
        visibility = None,
        tags = [],
        **kwargs):
    """A `go_binary`. `srcs` must be a `package main`."""
    _require_importpath(name, importpath)
    go_binary(
        name = name,
        srcs = srcs,
        importpath = importpath,
        deps = deps,
        embed = embed,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = _GO_TAGS + tags,
        **kwargs
    )

def go_test_lib(
        name,
        srcs,
        deps = [],
        embed = [],
        importpath = "",
        visibility = None,
        tags = [],
        size = "small",
        timeout = None,
        **kwargs):
    """A `go_test`.

    `size` is required by the Bazel test contract; `timeout` is deliberately
    left unset so Bazel derives it from `size` (small=60s, medium=300s,
    large=900s) instead of silently applying an arbitrary cap.
    """
    go_test(
        name = name,
        srcs = srcs,
        deps = deps,
        embed = embed,
        importpath = importpath,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = _GO_TAGS + tags + ["type=test"],
        size = size,
        **kwargs
    )

# -----------------------------------------------------------------------------
# Protobuf
# -----------------------------------------------------------------------------
# `proto_lib` and `proto_go` are NOT Gazelle-safe: use them only in a package
# marked `# gazelle:ignore`. `# gazelle:proto disable` stops Gazelle modelling the
# .proto layout, but its `load()` fixer still appends the canonical
# `load("@rules_proto//proto:defs.bzl", "proto_library")` and
# `load("@rules_go//proto:def.bzl", "go_proto_library")`, duplicating symbols
# that are deliberately routed through this file. See the module docstring.

def proto_lib(
        name,
        srcs,
        strip_import_prefix = None,
        deps = [],
        visibility = None,
        tags = [],
        **kwargs):
    """A `proto_library`.

    Args:
      strip_import_prefix: pass the package directory (for example `"/proto/common"`)
        when the .proto files import each other with paths relative to that
        directory, as in `import "common/common.proto"`. Passing `None` makes
        protoc see paths relative to the workspace root.
    """
    proto_library(
        name = name,
        srcs = srcs,
        deps = deps,
        strip_import_prefix = strip_import_prefix,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = _PROTO_TAGS + tags,
        **kwargs
    )

def proto_go(
        name,
        protos,
        importpath,
        deps = [],
        visibility = None,
        tags = [],
        **kwargs):
    """Generate Go bindings for `protos` with `go_proto_library`.

    `go_proto_library` is required here, not optional. A previous version of this
    macro tried `go_library(srcs = protos)`, which puts proto_library *labels*
    into a go_library's `srcs`; that fails analysis with
    "'//proto/common:common' does not produce any go_library srcs files".

    The legacy `go_proto_library` is used rather than the newer
    `go_proto_compiler` because it needs no external plugin toolchain.
    """
    _require_importpath(name, importpath)
    go_proto_library(
        name = name,
        protos = protos,
        importpath = importpath,
        deps = deps,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = _GO_TAGS + _PROTO_TAGS + tags,
        **kwargs
    )

# Aliases kept so that existing call sites and documentation keep working.
go_proto = proto_go
go_proto_lib = proto_go

# -----------------------------------------------------------------------------
# Shell
# -----------------------------------------------------------------------------

def sh_bin(
        name,
        srcs,
        visibility = None,
        tags = [],
        **kwargs):
    sh_binary(
        name = name,
        srcs = srcs,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = _SHELL_TAGS + tags,
        **kwargs
    )

def sh_library_target(
        name,
        srcs,
        deps = [],
        visibility = None,
        tags = [],
        **kwargs):
    """A `sh_library`. Shell rules are loaded eagerly by the sh_* toolchain."""
    sh_library(
        name = name,
        srcs = srcs,
        deps = deps,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = _SHELL_TAGS + tags,
        **kwargs
    )

def sh_test_target(
        name,
        srcs,
        deps = [],
        data = [],
        visibility = None,
        tags = [],
        size = "small",
        **kwargs):
    """A `sh_test`. Leave `timeout` unset so it derives from `size`."""
    sh_test(
        name = name,
        srcs = srcs,
        deps = deps,
        data = data,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = _SHELL_TAGS + tags + ["type=test"],
        size = size,
        **kwargs
    )

# -----------------------------------------------------------------------------
# Generic targets
# -----------------------------------------------------------------------------

def filegroup_lib(
        name,
        srcs = [],
        visibility = None,
        tags = [],
        **kwargs):
    """A `filegroup`. Use only for non-compiled assets (docs, templates, ...)."""
    native.filegroup(
        name = name,
        srcs = srcs,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = tags,
        **kwargs
    )

def template_dir(
        name,
        path,
        visibility = None,
        tags = [],
        **kwargs):
    """Expose a directory of scaffolding templates as a build target.

    `path` is relative to the calling package, because `native.glob` is.
    """
    native.filegroup(
        name = name,
        srcs = native.glob([path + "/**"], allow_empty = True),
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = tags + ["type=template"],
        **kwargs
    )

def config_struct(
        name,
        visibility = None,
        tags = [],
        **kwargs):
    """A named, tag-carrying marker target describing a configuration.

    Bazel has no `struct` rule, so this is an empty `filegroup`: it carries the
    configuration in its tags and gives callers a label to depend on and query.
    """
    native.filegroup(
        name = name,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = tags + ["type=config"],
        **kwargs
    )

def alias_target(
        name,
        actual,
        visibility = None,
        **kwargs):
    native.alias(
        name = name,
        actual = actual,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        **kwargs
    )

# -----------------------------------------------------------------------------
# Team Topologies
# -----------------------------------------------------------------------------

def team_boundary(
        name,
        team,
        srcs = [],
        public_api = True,
        visibility = None,
        tags = [],
        **kwargs):
    """Wrap a team's surface and apply the visibility that matches its intent.

      * `public_api = True`  - this is the team's supported entry point, visible
        to every team. It also carries the `public_api` tag, which is the one
        documented escape hatch that lets a `team=`-tagged target be public
        under //bazel/aspects:aspects.bzl%team_boundary_aspect.
      * `public_api = False` - team internals, visible only to `team`.

    Passing an explicit `visibility` overrides both defaults, and the aspect
    then judges the result on its merits.
    """
    native.filegroup(
        name = name,
        srcs = srcs,
        visibility = _resolve_visibility(
            visibility,
            _public_visibility() if public_api else _team_visibility(team),
        ),
        tags = tags + (
            ["team=" + team, "team_boundary", "public_api"] if public_api else ["team=" + team, "team_boundary"]
        ),
        **kwargs
    )

def platform_visibility(
        name,
        srcs = [],
        public = True,
        visibility = None,
        tags = [],
        **kwargs):
    """Expose a platform capability.

    Platform code is the one place where a wide public surface is correct: it
    exists to be consumed by every team.

    `visibility` must be declared as a parameter. Without it, the name
    `visibility` inside the body resolves to the BUILD-language builtin function
    of the same name, and the generated filegroup fails analysis with
    "expected value of type 'list(label)' ... but got <built-in function
    visibility>".
    """
    native.filegroup(
        name = name,
        srcs = srcs,
        visibility = _resolve_visibility(
            visibility,
            _public_visibility() if public else _platform_visibility(),
        ),
        tags = tags + ["layer=platform"],
        **kwargs
    )

# -----------------------------------------------------------------------------
# Platform capabilities
# -----------------------------------------------------------------------------
# Each capability lives in its own Go package, so these macros just apply the
# shared conventions. They deliberately take `srcs`/`importpath` explicitly
# rather than guessing a file name.
#
# These macros add nothing to the target except `tags`, and every check in
# //bazel/aspects:aspects.bzl keys on those tags rather than on the rule kind.
# They are also NOT Gazelle-indexable: each would have to displace `go_lib` from
# `config.KindMap`, and an unindexed library makes Gazelle silently delete real
# `deps` entries and shell out to `go`. In a package Gazelle manages, prefer a
# plain `go_lib` with the same tags - see the module docstring. //platform/*
# does exactly that.

def logging_lib(
        name,
        srcs,
        importpath,
        severity = "INFO",
        visibility = None,
        tags = [],
        **kwargs):
    go_lib(
        name = name,
        srcs = srcs,
        importpath = importpath,
        visibility = visibility,
        tags = tags + [_LAYER_PLATFORM, "capability=logging", "severity=" + severity],
        **kwargs
    )

def metrics_lib(
        name,
        srcs,
        importpath,
        service_name,
        visibility = None,
        tags = [],
        **kwargs):
    go_lib(
        name = name,
        srcs = srcs,
        importpath = importpath,
        visibility = visibility,
        tags = tags + [_LAYER_PLATFORM, "capability=metrics", "service_name=" + service_name],
        **kwargs
    )

def tracing_lib(
        name,
        srcs,
        importpath,
        sampler_rate = 0.1,
        service_name = "",
        visibility = None,
        tags = [],
        **kwargs):
    go_lib(
        name = name,
        srcs = srcs,
        importpath = importpath,
        visibility = visibility,
        tags = tags + [
            _LAYER_PLATFORM,
            "capability=tracing",
            "sampler_rate=" + str(sampler_rate),
            "service_name=" + service_name,
        ],
        **kwargs
    )

def auth_lib(
        name,
        srcs,
        importpath,
        providers = [],
        jwt_alg = "RS256",
        visibility = None,
        tags = [],
        **kwargs):
    go_lib(
        name = name,
        srcs = srcs,
        importpath = importpath,
        visibility = visibility,
        tags = tags + [
            _LAYER_PLATFORM,
            "capability=auth",
            "providers=" + ",".join(providers),
            "jwt_alg=" + jwt_alg,
        ],
        **kwargs
    )

def secrets_lib(
        name,
        srcs,
        importpath,
        backend = "vault",
        rotate_interval_hours = 24,
        visibility = None,
        tags = [],
        **kwargs):
    go_lib(
        name = name,
        srcs = srcs,
        importpath = importpath,
        visibility = visibility,
        tags = tags + [
            _LAYER_PLATFORM,
            "capability=secrets",
            "backend=" + backend,
            "rotate_interval_hours=" + str(rotate_interval_hours),
        ],
        **kwargs
    )

def policy_lib(
        name,
        srcs,
        importpath,
        language = "rego",
        decision_points = [],
        visibility = None,
        tags = [],
        **kwargs):
    """A policy capability: Go evaluation engine plus its policy sources."""
    go_lib(
        name = name,
        srcs = srcs,
        importpath = importpath,
        visibility = visibility,
        tags = tags + [
            _LAYER_PLATFORM,
            "capability=policy",
            "language=" + language,
            "decision_points=" + ",".join(decision_points),
        ],
        **kwargs
    )

# -----------------------------------------------------------------------------
# Developer experience
# -----------------------------------------------------------------------------

def devtool_bin(
        name,
        srcs,
        importpath,
        binary_name = "",
        commands = [],
        visibility = None,
        tags = [],
        **kwargs):
    """A `go_bin` that also records its command surface as tags.

    NOT Gazelle-safe: use only in a package marked `# gazelle:ignore`, because it
    is a `package main` target *and* Gazelle cannot index the name
    `devtool_bin`. It adds nothing to the target beyond tags, so a plain
    `go_bin` with the same `tags` is equivalent and Gazelle-safe. See the module
    docstring.
    """
    go_bin(
        name = name,
        srcs = srcs,
        importpath = importpath,
        visibility = visibility,
        tags = tags + [
            _LAYER_PLATFORM,
            "type=devtool",
            "binary=" + (binary_name or name),
            "commands=" + ",".join(commands),
        ],
        **kwargs
    )

def codegen_lib(
        name,
        protos = [],
        importpath = "",
        language_targets = ["go"],
        visibility = None,
        tags = [],
        **kwargs):
    """Code generation entry point for a set of protos."""
    proto_go(
        name = name,
        protos = protos,
        importpath = importpath,
        visibility = visibility,
        tags = tags + [
            "type=codegen",
            "languages=" + ",".join(language_targets),
        ],
        **kwargs
    )

# -----------------------------------------------------------------------------
# Service scaffold
# -----------------------------------------------------------------------------

def go_service(
        name,
        srcs,
        importpath,
        service_name = "",
        test_srcs = [],
        deps = [],
        test_deps = [],
        visibility = None,
        tags = [],
        size = "small",
        **kwargs):
    """A service binary plus its colocated unit tests.

    NOT Gazelle-safe. Use only in a package marked `# gazelle:ignore`, for two
    independent reasons: Gazelle's model for a `package main` directory is a
    `go_binary` that embeds a `go_library`, so it splits this call in two, and it
    cannot index the name `go_service` at all. See the module docstring. The
    binary that //services/commerce/cmd/product_catalog actually uses is a plain
    `go_bin` with the same tags spelled out.

    Args:
      name: binary target name.
      srcs: the service's `package main` sources.
      importpath: full Go import path of the binary.
      service_name: logical service name, used for logs, metrics and tracing.
      test_srcs: test sources. When empty no test target is created, so a
        service without tests does not break `bazel test //...`.
      size: test size for the generated test target.
      **kwargs: forwarded to `go_binary`.
    """
    if not service_name:
        service_name = name

    go_bin(
        name = name,
        srcs = srcs,
        importpath = importpath,
        deps = deps,
        visibility = _resolve_visibility(visibility, _public_visibility()),
        tags = tags + ["type=service", "service=" + service_name],
        **kwargs
    )

    if test_srcs:
        go_test_lib(
            name = name + "_test",
            srcs = test_srcs,
            embed = [":" + name],
            deps = test_deps,
            visibility = _resolve_visibility(visibility, _public_visibility()),
            tags = tags + ["type=service_test", "service=" + service_name],
            size = size,
        )

# -----------------------------------------------------------------------------
# Container images
# -----------------------------------------------------------------------------

def docker_image(name, **kwargs):
    """Not implemented: this repository has no container rule set available.

    `rules_docker` is unmaintained and absent from the Bazel Central Registry,
    and its replacement (`rules_oci`) requires Bazel 8 or newer while this
    workspace pins Bazel 7.3.1. Rather than fail deep inside a dependency graph,
    say so here and point at the two supported ways forward:

      1. Stay on Bazel 7 and vendor `rules_docker` into `third-party/`.
      2. Upgrade to Bazel 8+ (`bazel mod deps --latest`) and add
         `bazel_dep(name = "rules_oci", version = "2.3.0")`, then replace this
         macro with an `oci_image` wrapping an `oci_pull`ed base image.
    """
    fail(("%s: docker_image() is not available. See the macro's docstring in " +
          "//build:macros.bzl for the two supported options.") % name)