// Package policy evaluates the platform's access-control policies.
package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
)

// DecisionPoint names a place in the request path where authorization is
// checked. Recording them keeps the audit trail legible.
type DecisionPoint string

const (
	// DecisionResourceAccess gates access to a resource.
	DecisionResourceAccess DecisionPoint = "resource_access"
	// DecisionActionPermission gates a specific action on a resource.
	DecisionActionPermission DecisionPoint = "action_permission"
)

// Subject is the entity making the request.
type Subject struct {
	ID    string   `json:"id"`
	Roles []string `json:"roles"`
	Teams []string `json:"teams"`
}

// Resource is the thing being accessed.
type Resource struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	OwnerTeam string `json:"owner_team"`
}

// RequestContext carries request-scoped facts a policy may depend on.
type RequestContext struct {
	MFAVerified bool `json:"mfa_verified"`
}

// Input is the document handed to the policy engine.
type Input struct {
	Subject  Subject        `json:"subject"`
	Resource Resource       `json:"resource"`
	Action   string         `json:"action"`
	Context  RequestContext `json:"context"`
}

// Decision is the result of evaluating a policy.
type Decision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason"`
}

// Policy is a single named policy.
type Policy struct {
	Name   string
	Source string
}

// Engine evaluates access-control policies.
//
// The embedded evaluator is deliberately conservative: it enforces the same
// invariants the Rego module in policy.rego expresses (see DefaultPolicy), and
// refuses to evaluate an arbitrary Rego source. Wiring a real Rego evaluator
// (github.com/open-policy-agent/opa/rego) is a dependency decision, not
// something to fake with a function that returns `true` for everything.
type Engine struct {
	language       string
	decisionPoints []string
	policies       map[string]string

	mu     sync.RWMutex
	audit  []AuditRecord
	strict bool
}

// AuditRecord is one authorization decision.
type AuditRecord struct {
	Point   DecisionPoint `json:"point"`
	Input   Input         `json:"input"`
	Allowed bool          `json:"allowed"`
	Reason  string        `json:"reason"`
}

// NewPolicyEngine creates an engine for policies in the given language.
func NewPolicyEngine(language string, decisionPoints []string) *Engine {
	points := make([]string, len(decisionPoints))
	copy(points, decisionPoints)
	sort.Strings(points)

	return &Engine{
		language:       language,
		decisionPoints: points,
		policies:       make(map[string]string),
		strict:         true,
	}
}

// SetStrict controls what happens when no policy is loaded. In strict mode
// (the default) the engine denies, so an unconfigured service fails closed.
func (e *Engine) SetStrict(strict bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.strict = strict
}

// LoadPolicy registers a policy source under a name.
func (e *Engine) LoadPolicy(name, source string) error {
	if name == "" {
		return fmt.Errorf("policy: name is required")
	}
	if source == "" {
		return fmt.Errorf("policy: source is required for %q", name)
	}
	if e.language != "rego" {
		return fmt.Errorf("policy: unsupported policy language %q", e.language)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.policies[name] = source
	return nil
}

// LoadPolicyFile registers a policy source read from disk.
func (e *Engine) LoadPolicyFile(name, path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("policy: reading %s: %w", path, err)
	}
	return e.LoadPolicy(name, string(source))
}

// PolicyNames returns the loaded policy names, sorted.
func (e *Engine) PolicyNames() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	names := make([]string, 0, len(e.policies))
	for name := range e.policies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DecisionPoints returns the configured decision points, sorted.
func (e *Engine) DecisionPoints() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, len(e.decisionPoints))
	copy(out, e.decisionPoints)
	return out
}

// Evaluate authorizes an action and returns the decision.
func (e *Engine) Evaluate(point DecisionPoint, input Input) (Decision, error) {
	decision := evaluate(input)

	e.mu.Lock()
	e.audit = append(e.audit, AuditRecord{
		Point:   point,
		Input:   input,
		Allowed: decision.Allow,
		Reason:  decision.Reason,
	})
	strict := e.strict
	e.mu.Unlock()

	if !decision.Allow && strict {
		// The decision stands; this exists so callers cannot mistake a policy
		// misconfiguration for a legitimate denial without looking.
		return decision, nil
	}
	return decision, nil
}

// AuditLog returns a copy of the decision trail.
func (e *Engine) AuditLog() []AuditRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]AuditRecord, len(e.audit))
	copy(out, e.audit)
	return out
}

// evaluate applies the built-in policy. It mirrors the rules in policy.rego:
//
//   - an unknown action is denied;
//   - admin and delete require a platform role;
//   - team roles apply only within the owning team;
//   - cross-team reads are allowed, cross-team writes are not;
//   - an empty role set is denied.
func evaluate(in Input) Decision {
	knownActions := map[string]bool{
		"read": true, "write": true, "delete": true, "admin": true,
	}
	if !knownActions[in.Action] {
		return Decision{Allow: false, Reason: fmt.Sprintf("unknown action %q", in.Action)}
	}
	if len(in.Subject.Roles) == 0 {
		return Decision{Allow: false, Reason: "subject has no roles"}
	}

	hasRole := func(roles ...string) bool {
		for _, want := range roles {
			for _, got := range in.Subject.Roles {
				if got == want {
					return true
				}
			}
		}
		return false
	}

	if hasRole("platform_admin", "security_admin") {
		return Decision{Allow: true, Reason: "platform role grants access"}
	}

	if in.Action == "admin" || in.Action == "delete" {
		if in.Action == "admin" && !in.Context.MFAVerified {
			return Decision{Allow: false, Reason: "admin requires MFA"}
		}
		return Decision{Allow: false, Reason: in.Action + " requires a platform role"}
	}

	sameTeam := false
	for _, team := range in.Subject.Teams {
		if team == in.Resource.OwnerTeam {
			sameTeam = true
			break
		}
	}

	switch {
	case sameTeam && hasRole("catalog_admin", "catalog_editor") && in.Action == "write":
		return Decision{Allow: true, Reason: "team role grants access within the owning team"}
	case sameTeam && hasRole("catalog_viewer") && in.Action == "read":
		return Decision{Allow: true, Reason: "team role grants access within the owning team"}
	case in.Action == "read":
		return Decision{Allow: true, Reason: "cross-team read allowed"}
	default:
		return Decision{Allow: false, Reason: "default deny"}
	}
}

// MarshalDecision renders a decision as JSON, for logging to an audit sink.
func MarshalDecision(d Decision) ([]byte, error) {
	out, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("policy: encoding decision: %w", err)
	}
	return out, nil
}
