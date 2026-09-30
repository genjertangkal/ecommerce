"""Cross-cutting analysis-time policies for the Ecommerce monorepo.

An *aspect* is the right tool for a check that must hold for every target
somewhere in the dependency graph, because it is applied by the build system
rather than by remembering to call something.

Two rules govern everything in this file:

1. Starlark macros can call rules, but **a .bzl file cannot instantiate a rule
   at top level**. Anything of the form `bool_flag(name = ...)` directly in a
   .bzl file is a load error. Build settings belong in a BUILD file; see
   //bazel/aspects/BUILD.bazel.
2. Inside an aspect implementation the attributes of the *underlying* rule are
   reached through `ctx.rule.attr`, never through `ctx.attr` / `ctx.files`.
   `ctx.files.srcs` refers to the aspect's own attributes, and an aspect
   generally has none.

Every aspect here is a pure analysis-time check: no external tool is executed,
so `bazel build //...` stays fast and the checks run everywhere, including in
CI and in `--config=ci` runs that skip other tooling.

Applying them
-------------
An aspect defined in a .bzl file is a Starlark value, not a target, so it is
addressed on the command line with the `//<file.bzl>%<symbol>` syntax:

    # one aspect, one target pattern
    bazel build //services/... \
        --aspects=//bazel/aspects:aspects.bzl%team_boundary_aspect

    # all of them, for everything -- this is what `--config=lint` does
    bazel build //... --config=lint

(There is no `aspect_target` *rule* to call from a BUILD file: it is absent from
Bazel's builtin namespace in 7.x, 8.x and 9.x alike, so a macro that tries
`native.aspect_target(...)` fails with "no native function or rule".)
"""

# Go module path from go.mod. Any Go target's importpath must be
# "<module_prefix>/<package path>".
_DEFAULT_IMPORTPATH_PREFIX = "github.com/ecommerce"

# Generated code lives in its own tree, keyed by proto package, and never
# mirrors the Bazel package path. Without this separation a generated library
# and a hand-written one can end up with the same Go import path, which produces
# a duplicate-package error that points at neither.
_GENERATED_IMPORTPATH_PREFIX = _DEFAULT_IMPORTPATH_PREFIX + "/genproto/"

# Visibility labels that are always acceptable, regardless of team. Stored
# without a leading `//` so that they can be compared against canonical labels.
_ALWAYS_VISIBLE = [
    "visibility:private",
    "teams:platform",
]

def _local_label(label):
    """Reduce a visibility entry to a comparable `package:target` string.

    Two things make this necessary:

    1. `visibility` holds `Label` objects, not strings, so it must be stringified.
    2. Bazel canonicalises them, so `//teams:commerce` arrives as
       `@@//teams:commerce`. Comparing that against the literal
       `//teams:commerce` never matches, which would make this check report
       every team-private target as a leak.
    """
    text = str(label)
    idx = text.find("//")
    return text[idx + 2:] if idx >= 0 else text

def _tag_value(tags, key):
    """Return the value of the first `key=value` entry in `tags`, or None."""
    prefix = key + "="
    for tag in tags:
        if tag.startswith(prefix):
            return tag[len(prefix):]
    return None

def _has_tag_value(tags, key):
    """True when a `key=value` entry exists. Use this for tags that carry data."""
    return _tag_value(tags, key) != None

def _has_flag_tag(tags, key):
    """True when the bare tag `key` is present, e.g. `layer=platform`.

    This is deliberately separate from `_has_tag_value`. A flag tag has no
    `=value` suffix, so a lookup that builds the prefix `key + "="` can never
    match one: passing "layer=platform" to a value-matching helper silently
    returned False for every target, and the aspect guarded by it never ran.
    """
    return key in tags

def _attr(ctx, name, default):
    """Read an attribute of the rule this aspect is attached to.

    `ctx.rule` only exposes the attributes the *concrete* rule declares, and an
    aspect can be applied to anything, so every lookup has to tolerate a missing
    attribute. Note also that `ctx.rule` has no `label` field: the label of the
    target being visited is `ctx.label`.
    """
    return getattr(ctx.rule.attr, name, default)

def _aspect_tags(ctx):
    """Tags of the underlying rule. Empty for rules with no `tags` attribute."""
    return _attr(ctx, "tags", [])

def _aspect_visibility(ctx):
    """Visibility of the underlying rule.

    An empty list means "use the package default", which an aspect cannot see,
    so such targets are skipped rather than reported as violations.
    """
    return _attr(ctx, "visibility", [])

def _entry_package(entry):
    """Return the Bazel package of one `label_list` entry, or "" if unknown.

    Entries in a `label_list` are not uniform, and which type arrives depends on
    the *defining* rule:

      * a Starlark rule such as `go_library` exposes `Target` objects, which
        carry no `package` field - the label is one hop away in `.label`;
      * a native rule such as `filegroup` exposes `File` objects, which do have
        `.package` directly.

    Reading `.package` straight off the entry therefore returns "" for every
    `go_library` dependency, which silently disabled the service-isolation check
    for exactly the rules it exists to police. `type(x) == "Target"` is the
    documented way to tell the two apart.
    """
    if type(entry) == "Target":
        return entry.label.package or ""
    return getattr(entry, "package", "") or ""

# -----------------------------------------------------------------------------
# Team boundary enforcement
# -----------------------------------------------------------------------------

def _team_boundary_aspect_impl(target, ctx):
    """A target tagged `team=X` must not be visible outside team X.

    This is the check that makes the Team Topologies boundaries real. A target
    tagged `team=commerce` that carries `//visibility:public` or
    `//teams:public` is a boundary leak: every other team can depend on
    commerce internals without any review.

    The single exception is a target that also carries the `public_api` tag,
    which //build:macros.bzl#team_boundary sets when a team deliberately exports
    a supported entry point. Requiring the tag keeps the exception explicit and
    greppable instead of weakening the rule for everybody.
    """
    tags = _aspect_tags(ctx)
    team = _tag_value(tags, "team")
    if team == None:
        return []

    visibility = _aspect_visibility(ctx)
    if not visibility:
        # Inherits the package default; nothing to verify from here.
        return []

    if _has_flag_tag(tags, "public_api"):
        return []

    own = "teams:" + team
    for label in visibility:
        local = _local_label(label)
        if local in _ALWAYS_VISIBLE:
            continue
        if local == own:
            continue
        # `__pkg__` and `__subpackages__` are the two package groups Bazel
        # generates on demand. Both are narrower than any team group, so they
        # are not a boundary leak: //services/commerce:__subpackages__ is how a
        # service's internal layers see each other.
        if local.endswith(":__pkg__") or local.endswith(":__subpackages__"):
            continue
        # Everything else is too wide for a team-private target.
        fail(("team boundary leak: %s is tagged team=%s but is visible to %s. " +
              "Either restrict it to //teams:%s (or //visibility:private), or " +
              "drop the team= tag if it really is a shared/platform target.") % (
            ctx.label,
            team,
            label,
            team,
        ))

    return []

team_boundary_aspect = aspect(
    implementation = _team_boundary_aspect_impl,
    doc = "Fails when a target tagged `team=X` is visible outside team X.",
    attrs = {},
)

# -----------------------------------------------------------------------------
# Platform API enforcement
# -----------------------------------------------------------------------------

def _platform_api_aspect_impl(target, ctx):
    """Everything under //platform must be consumable by every team.

    Platform capabilities exist to be used by all stream-aligned teams, so a
    private platform target is almost always a mistake.
    """
    package = ctx.label.package
    if not package.startswith("platform/"):
        return []

    if _has_flag_tag(_aspect_tags(ctx), "layer=platform"):
        visibility = _aspect_visibility(ctx)
        # `visibility` holds Label objects and Bazel canonicalises them, so the
        # entries have to go through _local_label() before they can be compared
        # to a literal. Comparing the raw labels against "//teams:public" never
        # matches, which made this aspect reject every platform target
        # unconditionally.
        visible_to = [_local_label(label) for label in visibility]
        if "teams:public" not in visible_to and "visibility:public" not in visible_to:
            fail(("platform target %s must be visible to //teams:public so that " +
                  "every team can consume the capability. Found: %s.") % (
                ctx.label,
                visibility or "<package default>",
            ))

    return []

platform_api_aspect = aspect(
    implementation = _platform_api_aspect_impl,
    doc = "Fails when a //platform target tagged layer=platform is not public.",
    attrs = {},
)

# -----------------------------------------------------------------------------
# Service isolation
# -----------------------------------------------------------------------------

def _service_isolation_aspect_impl(target, ctx):
    """A service may not depend on another service's implementation.

    Services collaborate through //proto contracts, not by importing each other's
    code. This keeps a service independently deployable and makes the proto
    boundary the real contract.
    """
    package = ctx.label.package
    if not package.startswith("services/"):
        return []

    own_service = _own_service(package)
    if own_service == None:
        return []

    deps = _attr(ctx, "deps", [])
    for dep in deps:
        dep_package = _entry_package(dep)
        if not dep_package.startswith("services/"):
            continue
        dep_service = _own_service(dep_package)
        if dep_service != None and dep_service != own_service:
            fail(("service isolation violation: %s (service %s) depends on %s " +
                  "(service %s). Services must talk through //proto contracts.") % (
                ctx.label,
                own_service,
                dep.label if type(dep) == "Target" else dep,
                dep_service,
            ))

    return []

def _own_service(package):
    """`services/commerce/foo` -> `commerce`; None if not a direct subpackage."""
    parts = package.split("/")
    if len(parts) < 2 or parts[0] != "services":
        return None
    return parts[1]

service_isolation_aspect = aspect(
    implementation = _service_isolation_aspect_impl,
    doc = "Fails when one //services/<team> target depends on another.",
    attrs = {},
)

# -----------------------------------------------------------------------------
# Go import path hygiene
# -----------------------------------------------------------------------------

def _go_importpath_aspect_impl(target, ctx):
    """A Go target's importpath must match its package path.

    Drift between a package's location and its import path is the single most
    common source of confusing Go errors in a monorepo: the compiler resolves
    imports by importpath, so a wrong value produces "no required module
    provides package" or a duplicate-package error that points nowhere useful.
    """
    importpath = _attr(ctx, "importpath", None)
    if importpath == None:
        return []

    if not importpath:
        # rules_go derives the importpath for go_binary/go_test; that is fine.
        return []

    package = ctx.label.package
    tags = _aspect_tags(ctx)

    if "language=proto" in tags:
        # Generated code: only the dedicated tree is acceptable, and the
        # remainder must be the proto package so that two protos with the same
        # message names cannot collide.
        if not importpath.startswith(_GENERATED_IMPORTPATH_PREFIX):
            fail(("generated Go code for %s must live under %s, but has " +
                  "importpath %r. Key the path on the proto package, e.g. %s" +
                  "ecommerce.common.") % (
                ctx.label,
                _GENERATED_IMPORTPATH_PREFIX,
                importpath,
                _GENERATED_IMPORTPATH_PREFIX,
            ))
        return []

    expected = _DEFAULT_IMPORTPATH_PREFIX
    if package:
        expected = expected + "/" + package

    if importpath != expected:
        fail(("wrong importpath for %s: got %r, want %r. Run " +
              "`bazel run //:gazelle` to regenerate BUILD files, or fix the " +
              "importpath argument of the macro.") % (
            ctx.label,
            importpath,
            expected,
        ))

    return []

go_importpath_aspect = aspect(
    implementation = _go_importpath_aspect_impl,
    doc = "Fails when a Go target's importpath does not match its package path.",
    attrs = {},
)
