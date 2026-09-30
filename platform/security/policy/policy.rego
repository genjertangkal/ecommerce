# Copyright 2026 The Ecommerce Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Access-control policy for the ecommerce platform.
#
# Evaluated by the policy engine in //platform/security/policy.
# The engine feeds it the attributes below and reads back a decision.
#
# Input shape:
#   {
#     "subject":  {"id": "u-1", "roles": ["catalog_admin"], "teams": ["commerce"]},
#     "resource": {"type": "product", "id": "p-1", "owner_team": "commerce"},
#     "action":   "write",
#     "context":  {"mfa_verified": true}
#   }

package ecommerce.security

import rego.v1

default decision := {
	"allow": false,
	"reason": "no policy matched; default deny",
}

# Actions recognised by the platform.
actions := {"read", "write", "delete", "admin"}

# Roles that may perform an action on a resource owned by any team.
platform_roles := {"platform_admin", "security_admin"}

# Roles that may write within their own team only.
team_writer_roles := {"catalog_admin", "catalog_editor"}

# Roles that may read within their own team.
team_reader_roles := {"catalog_viewer"}

# ---------------------------------------------------------------------------
# Denials
# ---------------------------------------------------------------------------

deny contains reason if {
	not input.action in actions
	reason := sprintf("unknown action %q", [input.action])
}

deny contains reason if {
	input.action == "admin"
	not input.subject.roles[_] in platform_roles
	reason := "admin requires a platform role"
}

# A subject with no roles at all gets nothing. Without this rule a subject with
# an empty roles array would fall through to the default deny anyway, but making
# it explicit keeps the audit reason actionable.
deny contains reason if {
	count(input.subject.roles) == 0
	reason := "subject has no roles"
}

# Deletion of a product is privileged even for team members.
deny contains reason if {
	input.action == "delete"
	not input.subject.roles[_] in platform_roles
	reason := "delete requires a platform role"
}

# Privileged actions require a second factor.
deny contains reason if {
	input.action == "admin"
	input.context.mfa_verified != true
	reason := "admin requires MFA"
}

# ---------------------------------------------------------------------------
# Allows
# ---------------------------------------------------------------------------

allow if {
	not deny[_]
	some role in input.subject.roles
	role in platform_roles
}

allow if {
	not deny[_]
	some role in input.subject.roles
	role in team_writer_roles
	same_team
	input.action in {"read", "write"}
}

allow if {
	not deny[_]
	some role in input.subject.roles
	role in team_reader_roles
	same_team
	input.action == "read"
}

# Cross-team reads are allowed for authenticated subjects: teams routinely need
# to read another team's catalogue. Cross-team writes are not.
allow if {
	not deny[_]
	count(input.subject.roles) > 0
	input.action == "read"
}

same_team if {
	input.subject.teams[_] == input.resource.owner_team
}

# ---------------------------------------------------------------------------
# Decision
# ---------------------------------------------------------------------------

decision := {
	"allow": true,
	"reason": reason,
} if {
	allow
	reason := explain[_]
} else := {
	"allow": false,
	"reason": reason,
} if {
	count(deny) > 0
	reason := concat("; ", sort([r | some r in deny]))
} else := {
	"allow": false,
	"reason": "default deny",
}

# Explanations make an allow auditable; without them an allow is just "true".
explain contains "platform role grants access" if {
	some role in input.subject.roles
	role in platform_roles
}

explain contains "team role grants access within the owning team" if {
	same_team
	some role in input.subject.roles
	role in team_writer_roles
	not role in platform_roles
}
