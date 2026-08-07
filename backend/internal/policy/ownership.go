package policy

import (
	"fmt"
	"sort"
	"strings"
)

// ObjectKind names a writable object in the ownership table.
type ObjectKind string

const (
	KindProject       ObjectKind = "project"
	KindMilestone     ObjectKind = "milestone"
	KindWorkItem      ObjectKind = "work_item"
	KindSubTask       ObjectKind = "sub_task"
	KindChecklistItem ObjectKind = "checklist_item"
	KindWorklog       ObjectKind = "worklog"
	KindDeviation     ObjectKind = "deviation"
	KindDiagram       ObjectKind = "diagram"
)

// Owner is which role owns a field. Deliberately not a ladder: a manager does
// not own `progress_pct` by outranking a developer. CONTRACT §4.2 is two
// columns, not two tiers, and the whole point of `status` being dev-owned is
// that it is the developer's report of reality — a manager overwriting it
// destroys the signal rather than correcting it.
type Owner string

const (
	OwnerManager   Owner = "manager"
	OwnerDeveloper Owner = "developer"
	OwnerAdmin     Owner = "admin"
)

// Scope is the relationship a developer must additionally have to the object.
type Scope string

const (
	// ScopeProject: membership plus the role is enough.
	ScopeProject Scope = "project"
	// ScopeAssigned: must be the assignee of the work item this write concerns
	// — the item itself, or the item owning the subtask or checklist item.
	ScopeAssigned Scope = "assigned"
	// ScopeAuthor: must have written the row.
	ScopeAuthor Scope = "author"
	// ScopeReporter: must have raised the row.
	ScopeReporter Scope = "reporter"
)

// Rule is one cell of the ownership table.
type Rule struct {
	Owner Owner
	Scope Scope
}

// object is one row of CONTRACT §4.2 plus its lifecycle rules.
type object struct {
	// fields maps a wire field name to its rule.
	fields map[string]Rule

	// allFields, when non-nil, applies to every field on this object. Used
	// where the contract says "all".
	allFields *Rule

	create Rule
	// del is nil where deletion is never permitted.
	del *Rule
}

// table is CONTRACT §4.2, as data.
//
// Field names are the wire names a PATCH body carries, because that is what is
// checked. Adding a field here is the entire change needed to enforce it: no
// handler is aware of any individual field's owner.
var table = map[ObjectKind]object{
	KindProject: {
		// "Project settings | all | —"
		allFields: &Rule{Owner: OwnerManager, Scope: ScopeProject},
		// Creating and deleting a project is deployment-level (§7.1).
		create: Rule{Owner: OwnerAdmin, Scope: ScopeProject},
		del:    &Rule{Owner: OwnerAdmin, Scope: ScopeProject},
	},

	KindMilestone: {
		fields: map[string]Rule{
			"name":       {OwnerManager, ScopeProject},
			"intent":     {OwnerManager, ScopeProject},
			"start_date": {OwnerManager, ScopeProject},
			"due_date":   {OwnerManager, ScopeProject},
			"status":     {OwnerManager, ScopeProject},
			"seq":        {OwnerManager, ScopeProject},
		},
		create: Rule{Owner: OwnerManager, Scope: ScopeProject},
		del:    &Rule{Owner: OwnerManager, Scope: ScopeProject},
	},

	KindWorkItem: {
		fields: map[string]Rule{
			"title":               {OwnerManager, ScopeProject},
			"intent":              {OwnerManager, ScopeProject},
			"acceptance_criteria": {OwnerManager, ScopeProject},
			"priority":            {OwnerManager, ScopeProject},
			"start_date":          {OwnerManager, ScopeProject},
			"due_date":            {OwnerManager, ScopeProject},
			"assignee_id":         {OwnerManager, ScopeProject},
			"milestone_id":        {OwnerManager, ScopeProject},
			"dependencies":        {OwnerManager, ScopeProject},
			"entity_refs":         {OwnerManager, ScopeProject},

			"status":       {OwnerDeveloper, ScopeAssigned},
			"progress_pct": {OwnerDeveloper, ScopeAssigned},
		},
		create: Rule{Owner: OwnerManager, Scope: ScopeProject},
		del:    &Rule{Owner: OwnerManager, Scope: ScopeProject},
	},

	KindSubTask: {
		fields: map[string]Rule{
			"title":       {OwnerDeveloper, ScopeAssigned},
			"status":      {OwnerDeveloper, ScopeAssigned},
			"assignee_id": {OwnerDeveloper, ScopeAssigned},
			"seq":         {OwnerDeveloper, ScopeAssigned},
		},
		create: Rule{Owner: OwnerDeveloper, Scope: ScopeAssigned},
		del:    &Rule{Owner: OwnerDeveloper, Scope: ScopeAssigned},
	},

	KindChecklistItem: {
		fields: map[string]Rule{
			"text":    {OwnerDeveloper, ScopeAssigned},
			"checked": {OwnerDeveloper, ScopeAssigned},
		},
		create: Rule{Owner: OwnerDeveloper, Scope: ScopeAssigned},
		del:    &Rule{Owner: OwnerDeveloper, Scope: ScopeAssigned},
	},

	KindWorklog: {
		fields: map[string]Rule{
			"body": {OwnerDeveloper, ScopeAuthor},
			"kind": {OwnerDeveloper, ScopeAuthor},
		},
		create: Rule{Owner: OwnerDeveloper, Scope: ScopeProject},
		// Worklogs are never deleted. Correct by adding — that is what makes
		// the log evidence rather than a current-state field (§6.5).
		del: nil,
	},

	KindDeviation: {
		fields: map[string]Rule{
			"resolution":  {OwnerManager, ScopeProject},
			"resolved_at": {OwnerManager, ScopeProject},

			"summary":  {OwnerDeveloper, ScopeReporter},
			"severity": {OwnerDeveloper, ScopeReporter},
		},
		create: Rule{Owner: OwnerDeveloper, Scope: ScopeProject},
		del:    &Rule{Owner: OwnerManager, Scope: ScopeProject},
	},

	KindDiagram: {
		// "Diagram (semantic) | all | —"
		allFields: &Rule{Owner: OwnerManager, Scope: ScopeProject},
		create:    Rule{Owner: OwnerManager, Scope: ScopeProject},
		del:       &Rule{Owner: OwnerManager, Scope: ScopeProject},
	},
}

// Rules returns the ownership rules for a kind, for tests and for the
// `/admin/policy` surface. The map is copied so a caller cannot edit the table.
func Rules(kind ObjectKind) (fields map[string]Rule, all *Rule, ok bool) {
	obj, ok := table[kind]
	if !ok {
		return nil, nil, false
	}
	out := make(map[string]Rule, len(obj.fields))
	for k, v := range obj.fields {
		out[k] = v
	}
	if obj.allFields != nil {
		cp := *obj.allFields
		return out, &cp, true
	}
	return out, nil, true
}

// Kinds returns every object kind in the table, sorted.
func Kinds() []ObjectKind {
	out := make([]ObjectKind, 0, len(table))
	for k := range table {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ── the checks ───────────────────────────────────────────────────────────────

// CheckWrite resolves every field being written to its owning role and scope,
// and returns the first denial.
//
// It is called before any write lands, with the complete set of fields the
// request touches. Checking field by field as each is applied would leave a
// partially-applied update behind the first denial.
func CheckWrite(kind ObjectKind, fields []string, s Subject) *Denial {
	obj, known := table[kind]
	if !known {
		return &Denial{
			Kind:    DenialInvalid,
			Object:  kind,
			Message: fmt.Sprintf("%q is not a writable object kind; the kinds are %s.", kind, joinKinds()),
		}
	}
	if d := s.checkMembership(kind); d != nil {
		return d
	}
	if s.EffectiveRole == RoleAdmin {
		return nil
	}

	// Sorted so that a request touching several disallowed fields always names
	// the same one. A denial that varies between identical requests is a
	// denial the caller learns nothing from.
	ordered := append([]string(nil), fields...)
	sort.Strings(ordered)

	for _, field := range ordered {
		rule, ok := obj.rule(field)
		if !ok {
			return &Denial{
				Kind:        DenialUnknownField,
				Object:      kind,
				Field:       field,
				ValidFields: obj.fieldNames(),
				Message: fmt.Sprintf("%q is not a field of %s. The writable fields are %s.",
					field, kind, strings.Join(obj.fieldNames(), ", ")),
			}
		}
		if d := s.allows(kind, field, rule); d != nil {
			return d
		}
	}
	return nil
}

// CheckCreate reports whether the subject may create an object of this kind.
func CheckCreate(kind ObjectKind, s Subject) *Denial {
	obj, known := table[kind]
	if !known {
		return &Denial{Kind: DenialInvalid, Object: kind,
			Message: fmt.Sprintf("%q is not a writable object kind; the kinds are %s.", kind, joinKinds())}
	}
	if d := s.checkMembership(kind); d != nil {
		return d
	}
	if s.EffectiveRole == RoleAdmin {
		return nil
	}
	return s.allows(kind, "", obj.create)
}

// CheckDelete reports whether the subject may delete an object of this kind.
//
// Deletion is always soft (§6.5 constraint 1); this decides who may set
// deleted_at, not whether a row leaves the database.
func CheckDelete(kind ObjectKind, s Subject) *Denial {
	obj, known := table[kind]
	if !known {
		return &Denial{Kind: DenialInvalid, Object: kind,
			Message: fmt.Sprintf("%q is not a writable object kind; the kinds are %s.", kind, joinKinds())}
	}
	if obj.del == nil {
		return &Denial{
			Kind:   DenialForbidden,
			Object: kind,
			Route:  "add a new one recording the correction",
			Message: fmt.Sprintf(
				"To correct a %s, add a new one recording the correction. They are never deleted — that is what makes the log evidence rather than a current-state field.",
				kind),
		}
	}
	if d := s.checkMembership(kind); d != nil {
		return d
	}
	if s.EffectiveRole == RoleAdmin {
		return nil
	}
	return s.allows(kind, "", *obj.del)
}

func (o object) rule(field string) (Rule, bool) {
	if o.allFields != nil {
		return *o.allFields, true
	}
	r, ok := o.fields[field]
	return r, ok
}

func (o object) fieldNames() []string {
	if o.allFields != nil {
		return nil
	}
	out := make([]string, 0, len(o.fields))
	for f := range o.fields {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// checkMembership rejects a caller who is not on the project at all. Admin is
// exempt: deployment-level authority does not depend on a membership row.
func (s Subject) checkMembership(kind ObjectKind) *Denial {
	if s.GlobalRole == RoleAdmin || s.IsMember {
		return nil
	}
	return &Denial{
		Kind:    DenialForbidden,
		Object:  kind,
		Route:   "ask an admin to add you to this project",
		Message: "To change anything in this project, ask an admin to add you to it. You are not currently a member.",
	}
}

// allows applies one rule. field is "" for lifecycle checks.
func (s Subject) allows(kind ObjectKind, field string, rule Rule) *Denial {
	switch rule.Owner {
	case OwnerAdmin:
		if s.GlobalRole == RoleAdmin {
			return nil
		}
		return s.deny(kind, field, rule, "ask an admin")

	case OwnerManager:
		if s.EffectiveRole == RoleManager {
			return nil
		}
		// The route that matters. A developer's way to change a manager-owned
		// field is a proposal, and saying so is what turns a refusal into an
		// action — for a human reading a toast and for an agent reading a tool
		// result alike (D34, §7.3).
		return s.deny(kind, field, rule, "raise a proposal")

	case OwnerDeveloper:
		// Not a ladder: a manager does not acquire a dev-owned field by
		// outranking its owner. The route out is to reassign, which is itself
		// a manager-owned field.
		if s.EffectiveRole != RoleDeveloper {
			return s.deny(kind, field, rule,
				"reassign the item to yourself first, or ask its assignee")
		}
		switch rule.Scope {
		case ScopeAssigned:
			if s.IsAssignee {
				return nil
			}
			return s.deny(kind, field, rule,
				"ask its assignee, or ask a manager to reassign it to you")
		case ScopeAuthor:
			if s.IsAuthor {
				return nil
			}
			return s.deny(kind, field, rule, "add a new entry recording the correction")
		case ScopeReporter:
			if s.IsReporter {
				return nil
			}
			return s.deny(kind, field, rule, "ask whoever reported it")
		default:
			return nil
		}
	}
	return s.deny(kind, field, rule, "ask an admin")
}

func (s Subject) deny(kind ObjectKind, field string, rule Rule, route string) *Denial {
	d := &Denial{
		Kind:          DenialFieldNotOwned,
		Object:        kind,
		Field:         field,
		OwningRole:    rule.Owner,
		Scope:         rule.Scope,
		EffectiveRole: s.EffectiveRole,
		Route:         route,
	}
	// Lead with the action, then the reason.
	if field == "" {
		d.Kind = DenialForbidden
		d.Message = fmt.Sprintf("To do this to a %s, %s. It is %s-owned, and your effective role on this project is %s.",
			kind, route, rule.Owner, s.EffectiveRole)
		return d
	}
	d.Message = fmt.Sprintf("To change %s, %s. It is %s-owned%s, and your effective role on this project is %s.",
		field, route, rule.Owner, scopeClause(rule.Scope), s.EffectiveRole)
	return d
}

func scopeClause(scope Scope) string {
	switch scope {
	case ScopeAssigned:
		return " on items assigned to you"
	case ScopeAuthor:
		return " by whoever wrote it"
	case ScopeReporter:
		return " by whoever reported it"
	}
	return ""
}

func joinKinds() string {
	kinds := Kinds()
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return strings.Join(out, ", ")
}

// ── denials ──────────────────────────────────────────────────────────────────

// DenialKind selects the contract error code the HTTP layer maps this to.
type DenialKind string

const (
	DenialFieldNotOwned DenialKind = "field_not_owned"
	DenialForbidden     DenialKind = "forbidden"
	DenialUnknownField  DenialKind = "validation_failed"
	DenialInvalid       DenialKind = "validation_failed"
)

// Denial is a refused write. Its Message is prompt text: it is returned
// verbatim to a human in a 403 body and to an agent as a tool result, and both
// audiences need the route forward more than they need the reason.
type Denial struct {
	Kind          DenialKind
	Object        ObjectKind
	Field         string
	OwningRole    Owner
	Scope         Scope
	EffectiveRole Role
	Route         string
	ValidFields   []string
	Message       string
}

func (d *Denial) Error() string { return d.Message }

// Details is the structured half of the contract's error body (§7.3).
func (d *Denial) Details() map[string]any {
	out := map[string]any{"object_kind": string(d.Object)}
	if d.Field != "" {
		out["field"] = d.Field
	}
	if d.OwningRole != "" {
		out["owning_role"] = string(d.OwningRole)
	}
	if d.Scope != "" {
		out["scope"] = string(d.Scope)
	}
	if d.EffectiveRole != "" {
		out["effective_role"] = string(d.EffectiveRole)
	}
	if d.Route != "" {
		out["route"] = d.Route
	}
	if len(d.ValidFields) > 0 {
		out["valid_fields"] = d.ValidFields
	}
	return out
}
