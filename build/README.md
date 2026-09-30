# Build Macros

Common Bazel macros for consistent build patterns across the monorepo.

## Macros

### `library(name, srcs, visibility, tags)`

Creates a generic library target using filegroup.

**Parameters:**
- `name` (required): Target name
- `srcs`: Source files (default: [])
- `visibility`: Visibility labels (default: ["//teams:public"])
- `tags`: Tags for categorization (default: [])

### `execute(name, visibility, tags)`

Creates an executable target for CLI tools.

**Parameters:**
- `name` (required): Target name
- `visibility`: Visibility labels (default: ["//teams:public"])
- `tags`: Tags including binary name

### `config_struct(name, visibility, tags)`

Creates a configuration struct target.

**Parameters:**
- `name` (required): Target name
- `visibility`: Visibility labels (default: ["//teams:public"])
- `tags`: Configuration metadata

### `template_dir(name, path, visibility, tags)`

Creates a template directory target.

**Parameters:**
- `name` (required): Target name
- `path`: Template directory path
- `visibility`: Visibility labels (default: ["//teams:public"])
- `tags`: Tags including "template"

### `team_boundary(name, team, public_api, tags)`

Enforces team boundary visibility.

**Parameters:**
- `name` (required): Target name
- `team`: Team name (commerce, identity, fulfillment, platform)
- `public_api`: Whether this is a public API (default: True)
- `tags`: Additional tags

## Usage

```python
load("//build:macros.bzl", "library", "execute", "team_boundary")

library(
    name = "my_util",
    srcs = ["util.go"],
    tags = ["language=go"],
)

execute(
    name = "my_cli",
    tags = ["binary=my-cli"],
)

team_boundary(
    name = "my_service_api",
    team = "commerce",
    public_api = True,
)
```