Teams

Stream-aligned team boundaries.

Each directory under this area represents a business capability owned by a stream-aligned team.

Team Topology

Team type: Stream-aligned

Primary responsibility: End-to-end delivery of a business capability

Primary interaction with Platform: X-as-a-Service

Interaction with Enabling: Facilitation

Interaction with Complicated Subsystems: Explicit contracts or X-as-a-Service

Structure
teams/
├── commerce/
├── identity/
└── fulfillment/

Ownership

Each team owns:

Business behavior

Domain decisions

Public APIs

Domain-specific tests

Operational responsibility for its capabilities

Boundaries

A team should not directly depend on another team's internal implementation.

Preferred:

Commerce
   │
   ▼
Identity Public API


Avoid:

Commerce
   │
   ▼
Identity Internal Implementation

Shared Libraries

Do not move business logic into libs/ simply because multiple teams currently use it.

First establish whether the functionality belongs to a domain owner.

A shared technical primitive may belong in:

libs/


A business capability normally belongs in its owning team.

Bazel Visibility

Team boundaries should be enforced through Bazel visibility where practical.

Public targets should be intentionally exposed.

Internal implementation targets should remain private.

Development

Each team directory contains its own README describing:

Team mission

Business capabilities

Ownership

Public APIs

Dependencies

Operational responsibilities

Architecture decisions

Related Documentation

docs/team-topologies/

docs/architecture/

proto/contracts/

platform/
