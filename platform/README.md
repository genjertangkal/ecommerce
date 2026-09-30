Platform

Internal platform capabilities consumed by stream-aligned teams.

The Platform Team treats these capabilities as internal products. The goal is to provide a reliable paved road that reduces cognitive load without taking ownership of business capabilities away from stream-aligned teams.

Team Topology

Team type: Platform

Primary interaction: X-as-a-Service

Secondary interaction: Collaboration and Facilitation when required

Responsibilities

The Platform area provides reusable engineering capabilities such as:

Build infrastructure

Developer experience

Observability

Security

Infrastructure automation

Boundaries

Platform components should provide technical capabilities.

They should not implement business-domain behavior belonging to stream-aligned teams.

For example:

Correct:

Commerce Team
      │
      ├── Commerce business logic
      │
      └── uses
             │
             ▼
       Platform Logging


Avoid:

Commerce Team
      │
      ▼
Platform
      │
      └── implements Commerce pricing logic


Business ownership remains with the stream-aligned team.

Dependency Model

Stream-aligned teams may consume stable platform APIs.

Stream-aligned Team
        │
        │ X-as-a-Service
        ▼
     Platform


Platform implementations should remain private unless explicitly exposed as a supported API.

Platform Product Principles
Self-service

Teams should be able to consume platform capabilities without manual intervention.

Golden paths

The platform should provide supported defaults for common engineering workflows.

Safe escape hatches

Teams may deviate from the golden path when necessary, but deviations should be explicit and documented.

Backward compatibility

Breaking changes to platform APIs require an explicit migration strategy.

Ownership

The Platform Team owns this directory.

Individual subdirectories may have dedicated maintainers.

Bazel

Platform components must expose well-defined Bazel targets.

Example:

bazel build //platform/observability/...
bazel test //platform/observability/...

Related Documentation

docs/team-topologies/

docs/architecture/

platform/build/

platform/developer-experience/
