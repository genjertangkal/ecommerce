module github.com/ecommerce

go 1.23.0

// This module intentionally has no external requirements yet: every package in
// the monorepo currently builds against the Go standard library only.
//
// To add a third-party dependency:
//   1. `go get <module>@<version>`
//   2. Append the generated repo name to the `use_repo(go_deps, ...)` call in
//      MODULE.bazel (the name is the module path lowercased, with `/` and `.`
//      replaced by `_`, e.g. github.com/redis/go-redis/v9 -> com_github_redis_go_redis_v9)
//   3. `bazel mod tidy`
//
// Gazelle maps each Go import to the corresponding Bazel label automatically,
// so BUILD files do not need to be edited by hand.
