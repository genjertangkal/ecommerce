# Bazel Monorepo Documentation

## Overview

This document describes the enhanced Bazel monorepo foundation for the ecommerce project, following Team Topologies patterns with Bzlmod.

## Architecture

### Directory Structure

```
ecommerce/
├── apps/                    # Application entry points
│   ├── desktop/
│   ├── mobile/
│   └── web/
│       ├── customer-web/
│       └── platform-web/
├── bazel/                   # Bazel infrastructure
│   ├── aspects/             # Cross-cutting aspects (lint, docs, license)
│   └── macros/              # Legacy macros (deprecated - use build/)
├── build/                   # Build utilities and macros
│   └── macros.bzl           # Common build macros (proper rules)
├── docs/                    # Documentation
├── libs/                    # Reusable technical libraries
│   ├── go/
│   │   ├── errors/
│   │   ├── http/
│   │   └── logging/
│   ├── java/
│   └── python/
├── platform/                # Internal platform (X-as-a-Service)
│   ├── developer-experience/
│   ├── infrastructure/
│   ├── observability/
│   └── security/
├── platforms/               # Platform definitions for cross-compilation
├── proto/                   # API contracts
│   ├── common/
│   └── contracts/
├── services/                # Deployable services
│   ├── commerce/
│   ├── identity/
│   ├── fulfillment/
│   ├── notification/
│   └── analytics/
├── subsystems/              # Complex subsystems
├── teams/                   # Team boundaries
├── third-party/             # External dependencies
├── toolchains/              # Toolchain configuration
└── tools/                   # Developer tools
```

## Bzlmod Configuration

### MODULE.bazel

The root `MODULE.bazel` declares all dependencies via Bzlmod:

```python
module(
    name = "ecommerce",
    version = "0.1.0",
)

bazel_dep(name = "rules_go", version = "0.50.0")
bazel_dep(name = "rules_proto", version = "7.1.0")
bazel_dep(name = "protobuf", version = "33.4")
bazel_dep(name = "rules_docker", version = "0.38.0")
bazel_dep(name = "rules_python", version = "1.7.0")
bazel_dep(name = "rules_java", version = "8.5.1")
bazel_dep(name = "bazel_skylib", version = "1.6.0")
bazel_dep(name = "gazelle", version = "0.37.0")
```

### .bazelversion

Pinned to `7.3.1` for reproducible builds.

### .bazelrc

Comprehensive configuration with:
- Bzlmod enabled
- Performance optimizations (parallel jobs, caching)
- Platform-specific configs (linux, macos, windows)
- CI/CD configuration
- Coverage configuration
- Remote caching/execution support

## Platform APIs

### Observability (`//platform/observability`)

Provides logging, metrics, and tracing as X-as-a-Service.

Each capability is its own Bazel package, so each is addressed as
`//platform/<area>/<capability>:<name>`.

**Targets:**
- `//platform/observability/logging:logging` - Structured logging library
- `//platform/observability/metrics:metrics` - Metrics collection (counters, gauges, histograms)
- `//platform/observability/tracing:tracing` - Distributed tracing
- `//platform/observability:observability` - Aggregate of all three
- `//platform/observability:config` - Global configuration

**Usage:**
```python
load("//build:macros.bzl", "go_lib", "go_test_lib")

# The capability macros (logging_lib, metrics_lib, tracing_lib) require a
# # gazelle:ignore directive in the package, because Gazelle can index only one
# macro name per builtin rule kind. In a package Gazelle manages, spell the
# capability tags out on a go_lib instead - see //platform/observability/logging.
go_lib(
    name = "my_service_logger",
    srcs = ["logging.go"],
    importpath = "github.com/myorg/myrepo/my_service_logger",
    tags = [
        "layer=platform",
        "capability=logging",
        "severity=INFO",
    ],
)
```

Note the `tags`: every check in `//bazel/aspects` keys on them, not on the rule
kind, so the resulting target is identical to the macro's.

### Security (`//platform/security`)

Provides authentication, authorization, and secrets management.

**Targets:**
- `//platform/security/auth:auth` - OAuth2, OpenID Connect, API key validation
- `//platform/security/policy:policy` - Rego-based policy engine (RBAC/ABAC)
- `//platform/security/secrets:secrets` - Secrets management
- `//platform/security:security` - Aggregate of all three
- `//platform/security:config` - Global security configuration

**Usage:**
```python
load("//build:macros.bzl", "go_lib")

go_lib(
    name = "my_auth",
    srcs = ["auth.go"],
    importpath = "github.com/myorg/myrepo/my_auth",
    tags = [
        "layer=platform",
        "capability=auth",
        "providers=oauth2,api-key",
        "jwt_alg=RS256",
    ],
)
```

### Developer Experience (`//platform/developer-experience`)

Provides CLI tools, code generators, and project templates.

**Targets:**
- `//platform/developer-experience:developer-experience` - Ecommerce CLI tool
- `//platform/developer-experience:cli` - Alias for the CLI
- `//platform/developer-experience:codegen` - Code generation entry point
- `//platform/developer-experience:templates` - Project templates
- `//platform/developer-experience:config` - Global DX configuration

The CLI is named after its directory, not `cli`, because that is the name Bazel
and Gazelle derive from the import path. `cli` is kept as an alias for
convenience.

## Reusable Libraries (`//libs`)

Technical utilities that don't represent business capabilities.

### Go Libraries

- `//libs/go/errors` - Standardized error handling with codes
- `//libs/go/http` - HTTP client/server utilities with retries
- `//libs/go/logging` - Structured JSON logging

## Proto Contracts (`//proto`)

Shared API definitions using Protocol Buffers.

- `//proto/common` - Common types (Timestamp, Metadata, Error, Pagination)
- `//proto/contracts` - Service-specific contracts (Product API)

**Usage:**
```python
load("//build:macros.bzl", "proto_lib", "proto_go")

proto_lib(
    name = "my_proto",
    srcs = ["my.proto"],
    deps = ["//proto/common:common"],
)

proto_go(
    name = "my_go_proto",
    protos = [":my_proto"],
    importpath = "github.com/ecommerce/my-proto",
)
```

## Services (`//services`)

Deployable microservices following standard patterns.

### Commerce Service (`//services/commerce`)

Product Catalog API with full CRUD operations.

**Targets:**
- `//services/commerce/cmd/product_catalog:product_catalog` - Main service binary
- `//services/commerce:product_proto` - This service's protobuf contract
- `//services/commerce:product_go_proto` - Generated Go bindings for the contract

The binary lives in its own package because `package main` needs its own import
path, and a BUILD file is what makes a directory a separate package.

Not present: a container image and an integration test target. `docker_image()`
in `//build:macros.bzl` fails with an explanation rather than silently doing
nothing - `rules_docker` is unmaintained and `rules_oci` needs Bazel 8, while
this workspace pins 7.3.1. See the `//services/commerce` BUILD file.

**Usage:**
```python
# # gazelle:ignore is required in this package. Gazelle models a `package main`
# directory as a go_binary that embeds a go_library, so it would otherwise split
# this into two targets sharing one Go import path. See the Gazelle section in
# //BUILD.bazel.
load("//build:macros.bzl", "go_bin")

go_bin(
    name = "product_catalog",
    srcs = ["main.go"],
    importpath = "github.com/ecommerce/services/commerce/cmd/product_catalog",
    deps = [
        "//libs/go/logging",
        "//platform/observability/logging:logging",
        "//services/commerce/internal/handler",
    ],
    tags = [
        "service=product-catalog",
        "team=commerce",
        "type=service",
    ],
)
```

`go_service` also exists in `//build:macros.bzl`, but it takes `srcs`, `importpath`
and `deps` - there are no `proto_deps` or `platform_deps` parameters - and it
needs `# gazelle:ignore` for the reason above.

## Team Boundaries (`//teams`)

Visibility enforcement following Team Topologies.

### Team Package Groups

- `//teams:public` - All packages (default)
- `//teams:commerce` - Commerce team packages
- `//teams:identity` - Identity team packages
- `//teams:fulfillment` - Fulfillment team packages
- `//teams:platform` - Platform team (full access)

### Visibility Patterns

```python
# Public API - visible to all teams
go_lib(
    name = "public_api",
    visibility = ["//teams:public"],
)

# Team-internal - only visible to owning team
go_lib(
    name = "internal_impl",
    visibility = ["//teams:commerce"],
)

# Platform exports - visible to all
go_lib(
    name = "platform_api",
    visibility = ["//teams:public"],
)
```

## Build Macros (`//build:macros.bzl`)

Common macros for consistent build patterns using proper Bazel rules.

### Go Macros

#### `go_lib(name, srcs, deps, importpath, visibility, tags)`

Create a Go library target with standard conventions.

```python
load("//build:macros.bzl", "go_lib")

go_lib(
    name = "my_lib",
    srcs = ["lib.go"],
    importpath = "github.com/ecommerce/my-lib",
    visibility = ["//teams:public"],
    tags = ["language=go", "type=utility"],
)
```

#### `go_bin(name, srcs, deps, importpath, visibility, tags)`

Create a Go binary target.

```python
load("//build:macros.bzl", "go_bin")

go_bin(
    name = "my_cli",
    srcs = ["main.go"],
    importpath = "github.com/ecommerce/my-cli",
    visibility = ["//teams:public"],
)
```

#### `go_test_lib(name, srcs, deps, importpath, visibility, tags)`

Create a Go test target.

```python
load("//build:macros.bzl", "go_test_lib")

go_test_lib(
    name = "my_lib_test",
    srcs = ["lib_test.go"],
    deps = [":my_lib"],
    size = "medium",
)
```

### Protobuf Macros

#### `proto_lib(name, srcs, deps, visibility, tags)`

Create a Protobuf library target.

```python
load("//build:macros.bzl", "proto_lib")

proto_lib(
    name = "my_proto",
    srcs = ["my.proto"],
    deps = ["//proto/common:common"],
)
```

#### `proto_go(name, protos, deps, importpath, visibility, tags)`

Generate Go code from protobuf definitions.

```python
load("//build:macros.bzl", "proto_go")

proto_go(
    name = "my_go_proto",
    protos = [":my_proto"],
    importpath = "github.com/ecommerce/my-proto",
)
```

### Docker Macros

#### `docker_image(name, base, files, entrypoint, ports, labels)`

Create a Docker container image.

```python
load("//build:macros.bzl", "docker_image")

docker_image(
    name = "my_service_image",
    base = "@base_images//:distroless_static",
    files = [":my_service"],
    entrypoint = ["/my_service"],
    ports = [8080],
    labels = {
        "org.opencontainers.image.title": "My Service",
    },
)
```

### Service Scaffold Macro

#### `go_service(name, srcs, importpath, service_name, test_srcs, deps, test_deps, visibility, tags, size)`

Create a service binary plus, when `test_srcs` is given, a colocated test target.

```python
load("//build:macros.bzl", "go_service")

go_service(
    name = "my_service",
    srcs = ["main.go"],
    importpath = "github.com/myorg/myrepo/cmd/my_service",
    service_name = "my-service",
    deps = [
        "//platform/observability/logging:logging",
        "//platform/security/auth:auth",
    ],
)
```

Requires `# gazelle:ignore` in the calling package - see the Gazelle section in
//BUILD.bazel.

## Cross-cutting Aspects (`//bazel/aspects`)

Reusable aspects for linting, documentation, and code quality.

### Available Aspects

- `go_lint_aspect` - Runs golangci-lint on Go sources
- `proto_lint_aspect` - Runs buf lint on Protobuf sources
- `doc_gen_aspect` - Generates documentation for Go and Protobuf
- `license_aspect` - Checks for license headers
- `dep_analysis_aspect` - Analyzes and visualizes dependencies
- `coverage_aspect` - Ensures code coverage collection

## Platforms & Toolchains

### Platform Definitions (`//platforms`)

Cross-compilation platform definitions:

- `//platforms:linux_x86_64` - Linux x86_64
- `//platforms:linux_arm64` - Linux ARM64
- `//platforms:macos_arm64` - macOS ARM64
- `//platforms:macos_x86_64` - macOS x86_64
- `//platforms:windows_x86_64` - Windows x86_64

### Toolchain Configuration (`//toolchains`)

Toolchain definitions for:
- Go (auto-registered via rules_go)
- C++ (for CGO support)
- Protocol Buffer
- Docker
- Java

## Common Commands

```bash
# Build all targets
bazel build //...

# Build with specific config
bazel build //... --config=ci
bazel build //... --config=opt
bazel build //... --config=dev

# Build platform observability
bazel build //platform/observability/...

# Build all Go libraries
bazel build //libs/go/...

# Build proto contracts
bazel build //proto/...

# Build services
bazel build //services/...

# Run tests
bazel test //...

# Run tests with coverage
bazel coverage //...

# Query dependencies
bazel query 'deps(//platform/observability/logging:logging)'

# Query team boundaries
bazel query 'attr("visibility", "//teams:commerce", //...)'

# Generate dependency graph
bazel query "deps(//services/commerce/cmd/product_catalog:product_catalog)" --output=graph | dot -Tpng > deps.png

# Profile build
bazel build //... --profile=profile.json
bazel analyze-profile profile.json

# Run Gazelle to update BUILD files
bazel run //:gazelle
```

## CI/CD Integration

The `.bazelrc` includes CI-specific configurations:

```bash
# CI build
bazel build --config=ci //...

# CI test
bazel test --config=ci-test //...

# Coverage
bazel coverage --config=coverage //...
```

## Extending the Foundation

### Adding a New Platform Capability

1. Create directory under `platform/`
2. Add `BUILD.bazel` with platform macros
3. Implement the capability
4. Export via `//teams:public` visibility

### Adding a New Team

1. Add team package group in `//teams/BUILD.bazel`
2. Create team directory under `teams/`
3. Configure team-specific visibility in service BUILD files

### Adding a New Library

1. Create directory under `libs/<language>/`
2. Add `BUILD.bazel` with `go_lib()` macro
3. Implement library
4. Export via `//teams:public` visibility

### Adding a New Service

1. Create directory under `services/<team>/`
2. Add `BUILD.bazel` with `go_service()` macro
3. Define proto contracts in `proto/`
4. Implement service in `cmd/<service>/main.go`
5. Add Docker image with `docker_image()` macro

## Best Practices

1. **Platform vs Business Logic**: Platform provides technical capabilities only
2. **Visibility**: Default to private, explicitly expose public APIs
3. **Dependencies**: Prefer proto contracts over direct code dependencies
4. **Macros**: Use build macros for consistent patterns
5. **Stamping**: Use workspace status for build metadata
6. **Bzlmod**: Declare all dependencies in MODULE.bazel
7. **Proper Rules**: Use `go_library`, `go_binary`, `proto_library` instead of filegroup fallbacks
8. **Testing**: Use `go_test_lib` with appropriate size and timeout
9. **Docker**: Use distroless base images for production
10. **Aspects**: Apply cross-cutting concerns via aspects, not manual checks