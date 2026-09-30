Complicated Subsystems

Specialized technical subsystems requiring expertise that is not expected to be replicated inside every stream-aligned team.

Team Topology

Team type: Complicated Subsystem

Primary interaction: X-as-a-Service

Collaboration may be used for complex integration work

Examples
subsystems/
├── recommendation/
├── search/
└── risk-engine/

When Does Something Belong Here?

A component may belong here when it requires specialized expertise such as:

Advanced search algorithms

Machine learning infrastructure

Recommendation systems

Risk scoring

Specialized optimization

Highly specialized distributed systems

The criterion is not complexity alone.

A simple but business-critical component should remain with its stream-aligned team.

API Boundary

Consumers should interact through explicit contracts.

Stream Team
     │
     │ API / RPC / Library Contract
     ▼
Complicated Subsystem


Consumers should not depend on internal implementation details.

Ownership

Each subsystem must have an explicit owning team.

The owning team is responsible for:

Implementation

Reliability

Performance

Compatibility

Documentation

Migration strategy

Bazel

Subsystems must expose stable Bazel targets and enforce internal visibility.

Example:

bazel build //subsystems/search/...
bazel test //subsystems/search/...

Related Documentation

See each subsystem's local README for implementation-specific details.
