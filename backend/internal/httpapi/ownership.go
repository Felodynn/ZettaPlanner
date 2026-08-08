package httpapi

import (
	"context"
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// The field ownership table, published.
//
// **Why this exists at all.** A frontend has to know which controls to disable,
// and it has exactly two ways to find out: duplicate §4.2 in TypeScript, or ask.
// Duplicating it means two copies of the rule that decides who may change a due
// date, and the copies drift on the day somebody edits the table and not the
// UI — silently, in the direction of showing people controls that will 403.
//
// So the backend publishes the table it actually enforces. This endpoint reads
// `policy.Rules`, the same function `CheckWrite` reads. It cannot describe a
// rule the write path does not apply, because there is no second table for it
// to describe.
//
// **It is a hint, not a control.** Nothing here is enforcement: the answer is
// computed from role alone, and the per-row half — is this caller the assignee,
// the author, the reporter — is only knowable per row. The frontend combines
// them; the backend re-checks everything on the write regardless (D7). A
// disabled button that is the only thing standing between a developer and a
// manager-owned field is not a permission system. Recorded as D65.

// FieldRuleView is one field's owner and scope.
type FieldRuleView struct {
	Field string `json:"field"`
	Owner string `json:"owner" enum:"admin,manager,developer"`
	Scope string `json:"scope" enum:"project,assigned,author,reporter" doc:"What relationship the caller needs beyond the role. 'project' means membership is enough"`
	// WritableByRole is whether the caller's effective role can own this field
	// at all. False means no relationship to the row will help, and the route
	// is a proposal (§5.1).
	WritableByRole bool `json:"writable_by_role"`
	// NeedsRelationship is true when the role is right but the answer still
	// depends on the specific row.
	NeedsRelationship bool `json:"needs_relationship"`
}

// ObjectOwnershipView is one object kind's rules.
type ObjectOwnershipView struct {
	Kind string `json:"kind"`
	// AllFields is set when every field of this kind shares one rule, in which
	// case Fields is empty.
	AllFields *FieldRuleView  `json:"all_fields,omitempty"`
	Fields    []FieldRuleView `json:"fields"`
}

// FieldOwnershipBody is the published table plus who is asking.
type FieldOwnershipBody struct {
	EffectiveRole string                `json:"effective_role" enum:"admin,manager,developer"`
	Objects       []ObjectOwnershipView `json:"objects"`
}

func (s *Server) registerOwnership() {
	huma.Register(s.API, huma.Operation{
		OperationID: "get-field-ownership",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/field-ownership",
		Summary:     "The field ownership table (§4.2), as this caller sees it",
		Description: "Read from the same table the write path enforces, so it cannot describe a rule that is " +
			"not applied.\n\n" +
			"**This is a hint for rendering, never a control.** `scope` values other than `project` depend on " +
			"the specific row — whether the caller is that item's assignee, that worklog's author, that " +
			"deviation's reporter — which only the frontend knows per row. Every write is checked again on " +
			"the server (D7). Use it to disable controls and to explain why; never to decide whether a " +
			"write is allowed.",
		Tags: []string{"policy"},
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
	}) (*struct{ Body FieldOwnershipBody }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		// Get is the membership gate, and it is also what resolves the caller's
		// standing: the effective role here has to be the project's, because a
		// manager added to a project as a developer is a developer in it.
		if _, err := s.deps.Projects.Get(ctx, in.PID, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		role, err := s.deps.Projects.EffectiveRole(ctx, in.PID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}

		out := &struct{ Body FieldOwnershipBody }{}
		out.Body.EffectiveRole = string(role)
		out.Body.Objects = ownershipViews(role)
		return out, nil
	})
}

func ownershipViews(role policy.Role) []ObjectOwnershipView {
	var out []ObjectOwnershipView
	for _, kind := range policy.Kinds() {
		fields, all, ok := policy.Rules(kind)
		if !ok {
			continue
		}
		view := ObjectOwnershipView{Kind: string(kind), Fields: []FieldRuleView{}}
		if all != nil {
			r := ruleView("*", *all, role)
			view.AllFields = &r
			out = append(out, view)
			continue
		}
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			view.Fields = append(view.Fields, ruleView(name, fields[name], role))
		}
		out = append(out, view)
	}
	return out
}

func ruleView(field string, r policy.Rule, role policy.Role) FieldRuleView {
	// An admin's authority is deployment-level and a project membership cannot
	// take it away (§4.1), so the table's owner is satisfied for them
	// everywhere.
	writable := role == policy.RoleAdmin || string(role) == string(r.Owner)
	return FieldRuleView{
		Field:             field,
		Owner:             string(r.Owner),
		Scope:             string(r.Scope),
		WritableByRole:    writable,
		NeedsRelationship: writable && role != policy.RoleAdmin && r.Scope != policy.ScopeProject,
	}
}
