package agent

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/governance"
)

// The propose-time gate on what a plan change actually says.
//
// The applier has always checked this. Checking it **here as well** is the
// whole point of the file: a proposal is read by a person and agreed to, and
// discovering at that moment that `P0` is not a priority spends the one
// resource this system exists to save — and spends it twice, because the
// rejection travels back as a rationale the next turn has to reason about,
// having learned nothing it could not have learned for free. Every failure
// returned here is one the model can still fix on its own, in the same turn.
//
// **Values are checked, never rewritten.** `milestone_id: "Foundation"` stays
// the name the model wrote, because that is the text a reviewer reads in the
// diff and a UUID tells them nothing; the applier resolves it at approve time
// against the same rule this file validates against. Only *shape* is
// normalised — a list sent as `"a, b"` becomes `["a","b"]` — because shape is
// not something a human reads or agrees to.

// planChecker validates the changes of one proposal, in order.
//
// It is per-proposal rather than per-change because the changes are an ordered
// list and not a set: a proposal that creates a milestone and then puts six
// items under it is one decision, and the second half has to be allowed to name
// something the first half is still proposing.
type planChecker struct {
	s *session

	milestones     []*pl.Milestone
	haveMilestones bool
	people         []repo.MemberRow
	havePeople     bool
	entities       []string
	haveEntities   bool

	// Milestone names an earlier change in this same proposal creates, folded
	// for comparison.
	creating map[string]bool
}

func newPlanChecker(s *session) *planChecker {
	return &planChecker{s: s, creating: map[string]bool{}}
}

// changeCtx is the one change being checked. Carried as a value rather than
// threaded through five parameters, because most of the field checks need the
// op as well as the target — whether a field may be cleared is a fact about
// both.
type changeCtx struct {
	i      int
	target pr.ChangeTarget
	op     pr.Op
	// ref is what the change names as its subject: the dependency check needs
	// it in order to notice an item being pointed at itself.
	ref   string
	after map[string]any
}

// n is the change's number as a person counts them.
func (cc changeCtx) n() int { return cc.i + 1 }

// check validates one change's `after` and records what it creates.
func (c *planChecker) check(ctx context.Context, cc changeCtx) error {
	if cc.target == pr.ChangeSubTask && cc.op != pr.OpCreate {
		return refuse("change %d would %s a subtask. A subtask has no manager-owned field (§4.2), so there is "+
			"nothing on an existing one for a proposal to decide — its assignee edits it directly. "+
			"Propose creating one, or say what should change in the conversation.", cc.n(), cc.op)
	}
	if cc.op == pr.OpDelete {
		return nil
	}
	if len(cc.after) == 0 {
		// An update with an empty `after` applies nothing and reports success,
		// which is the one outcome worse than a refusal: the reviewer agreed to
		// something and got nothing, with no way to tell.
		if cc.op == pr.OpCreate {
			return refuse("change %d creates a %s but sets no fields. Send them in `after`; a %s needs at least `%s`.",
				cc.n(), cc.target, cc.target, requiredOnCreate(cc.target))
		}
		return refuse("change %d updates a %s without saying what to change. "+
			"Send the fields and their new values in `after`.", cc.n(), cc.target)
	}

	if err := c.unknownFields(cc); err != nil {
		return err
	}
	for _, field := range sortedKeys(cc.after) {
		if err := c.field(ctx, cc, field); err != nil {
			return err
		}
	}
	if err := c.dateOrder(cc); err != nil {
		return err
	}
	if cc.op == pr.OpCreate {
		if field := requiredOnCreate(cc.target); textOf(cc.after[field]) == "" {
			return refuse("change %d creates a %s with no `%s`, which is the one field it cannot be without.",
				cc.n(), cc.target, field)
		}
	}
	if cc.target == pr.ChangeMilestone && cc.op == pr.OpCreate {
		if name := textOf(cc.after["name"]); name != "" {
			c.creating[fold(name)] = true
		}
	}
	return nil
}

// requiredOnCreate is the field without which the row cannot be written. Taken
// from the domain validators rather than restated, so a create refused here is
// refused for the reason it would have failed for later.
func requiredOnCreate(target pr.ChangeTarget) string {
	if target == pr.ChangeMilestone {
		return "name"
	}
	return "title"
}

// unknownFields rejects a field the applier could not set.
//
// The list comes from §4.2 through the ownership table, so it cannot drift from
// what will actually apply.
func (c *planChecker) unknownFields(cc changeCtx) error {
	settable := governance.SettableFields(cc.target)
	known := make(map[string]bool, len(settable))
	for _, f := range settable {
		known[f] = true
	}
	var unknown []string
	for key := range cc.after {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return refuse("change %d sets %s on a %s, which %s not a field of one. The fields you can set are: %s.",
		cc.n(), strings.Join(quoted(unknown), ", "), cc.target, isAre(len(unknown)), strings.Join(settable, ", "))
}

// field validates one field's value against the same rule the write path uses.
func (c *planChecker) field(ctx context.Context, cc changeCtx, field string) error {
	v := cc.after[field]
	switch field {
	case "status":
		return c.status(cc, v)
	case "priority":
		return c.fail(cc.i, pl.ValidatePriority(pl.Priority(textOf(v))))
	case "start_date", "due_date":
		_, err := pl.ParseDate(textOf(v))
		return c.fail(cc.i, err)
	case "seq":
		_, err := c.wholeNumber(cc.i, field, v)
		return err
	case "progress_pct":
		n, err := c.wholeNumber(cc.i, field, v)
		if err != nil {
			return err
		}
		if n != nil && (*n < 0 || *n > 100) {
			return refuse("change %d sets progress_pct to %d; it is a percentage, so it runs from 0 to 100.", cc.n(), *n)
		}
		return nil
	case "milestone_id":
		return c.milestone(ctx, cc)
	case "assignee_id":
		return c.assignee(ctx, cc)
	case "entity_refs":
		return c.entityRefs(ctx, cc)
	case "dependencies":
		return c.dependencies(ctx, cc)
	}
	return c.text(cc.i, field, v)
}

// status enforces the vocabulary, which is not the same one at both levels: a
// milestone is never `blocked` or `review`, because those are facts about the
// items inside it.
//
// The empty string is a create defaulting to `todo` and an update trying to
// clear a field that has no cleared state. Distinguished here rather than left
// to the write path, where the second one fails after somebody has approved it.
func (c *planChecker) status(cc changeCtx, v any) error {
	s := textOf(v)
	if s == "" {
		if cc.op == pr.OpCreate {
			return nil
		}
		valid := pl.Statuses()
		if cc.target == pr.ChangeMilestone {
			return refuse("change %d clears the status of a %s, and there is no such state — "+
				"a %s is always in one of planned, active, done, dropped. "+
				"Leave the field out to leave it where it is.", cc.n(), cc.target, cc.target)
		}
		names := make([]string, 0, len(valid))
		for _, st := range valid {
			names = append(names, string(st))
		}
		return refuse("change %d clears the status of a %s, and there is no such state — "+
			"a %s is always in one of %s. Leave the field out to leave it where it is; "+
			"`dropped` is how you say the work is not going to happen.",
			cc.n(), cc.target, cc.target, strings.Join(names, ", "))
	}
	if cc.target == pr.ChangeMilestone {
		return c.fail(cc.i, pl.ValidateMilestoneStatus(pl.MilestoneStatus(s)))
	}
	return c.fail(cc.i, pl.ValidateStatus(pl.Status(s)))
}

// milestone checks that `milestone_id` names something that will exist when the
// proposal is applied.
//
// A **name** is what the model has: `get_plan` and assembled context both print
// milestones by name and neither prints an id, so a field called `milestone_id`
// that accepted only ids was a field the agent could not fill — which is how a
// plan ends up with milestones nothing is under. The name is what the applier
// resolves and what the reviewer reads, so it is what stays in `after`.
func (c *planChecker) milestone(ctx context.Context, cc changeCtx) error {
	raw := textOf(cc.after["milestone_id"])
	if raw == "" {
		// The empty string is how a proposal takes an item back out of its
		// milestone. Distinct from omitting the key, which leaves it alone.
		return nil
	}
	list, err := c.loadMilestones(ctx)
	if err != nil {
		return err
	}
	if id, err := uuid.Parse(raw); err == nil {
		for _, m := range list {
			if m.ID == id {
				return nil
			}
		}
		return refuse("change %d sets milestone_id to %s, which is not a milestone in this project. %s",
			cc.n(), raw, milestoneHint(list))
	}
	for _, m := range list {
		if fold(m.Name) == fold(raw) {
			return nil
		}
	}
	if c.creating[fold(raw)] {
		// Created by an earlier change in this same proposal. The changes apply
		// in order inside one transaction, so by the time this one runs the
		// milestone is there.
		return nil
	}
	return refuse("change %d puts this under the milestone %q, and there is no such milestone. %s",
		cc.n(), raw, milestoneHint(list))
}

func milestoneHint(list []*pl.Milestone) string {
	if len(list) == 0 {
		return "This project has no milestones yet: propose creating one in the same proposal — " +
			"a change with target `milestone` and op `create` — and name it here, or send \"\" to leave the item unfiled."
	}
	names := make([]string, 0, len(list))
	for _, m := range list {
		names = append(names, strconv.Quote(m.Name))
	}
	return "The milestones are: " + strings.Join(names, ", ") +
		". Name one of those, or propose creating a new one in this same proposal and name that."
}

// assignee checks that `assignee_id` names somebody on this project.
//
// Names and email addresses are accepted for the same reason milestone names
// are: nothing the agent can read prints a user id, so an id-only field is an
// unassignable one, and work nobody is assigned to is work nobody does.
func (c *planChecker) assignee(ctx context.Context, cc changeCtx) error {
	raw := textOf(cc.after["assignee_id"])
	if raw == "" {
		return nil // unassign
	}
	people, err := c.loadPeople(ctx)
	if err != nil {
		return err
	}
	if id, err := uuid.Parse(raw); err == nil {
		for _, m := range people {
			if m.UserID == id {
				return nil
			}
		}
		return refuse("change %d assigns this to %s, who is not a member of this project. %s", cc.n(), raw, peopleHint(people))
	}

	var matched []string
	for _, m := range people {
		if fold(m.Email) == fold(raw) || fold(m.DisplayName) == fold(raw) {
			matched = append(matched, m.Email)
		}
	}
	switch len(matched) {
	case 1:
		return nil
	case 0:
		return refuse("change %d assigns this to %q, who is not a member of this project. %s", cc.n(), raw, peopleHint(people))
	default:
		// Two people with the same display name. Guessing which one picks
		// somebody's work for them, which is worse than asking.
		return refuse("change %d assigns this to %q, and %d members answer to that name (%s). "+
			"Use the email address, which is unique.", cc.n(), raw, len(matched), strings.Join(matched, ", "))
	}
}

func peopleHint(people []repo.MemberRow) string {
	if len(people) == 0 {
		return "This project has no members to assign to."
	}
	who := make([]string, 0, len(people))
	for _, m := range people {
		who = append(who, fmt.Sprintf("%s <%s>", m.DisplayName, m.Email))
	}
	return "The members are: " + strings.Join(who, "; ") +
		". Name one by display name or email address, or send \"\" to leave it unassigned."
}

// entityRefs checks the architecture slugs an item claims to implement.
//
// The list is normalised in place, because a model that sent `"a, b"` for an
// array meant two slugs and refusing that teaches nothing about the plan. The
// slugs themselves are checked rather than coerced: an entity that does not
// exist is not a formatting mistake, and a ref list that quietly lost an entry
// is an item that silently stops appearing in that entity's context slice.
func (c *planChecker) entityRefs(ctx context.Context, cc changeCtx) error {
	slugs, ok := coerceList(cc.after["entity_refs"])
	if !ok {
		return refuse("change %d sets entity_refs to something that is not a list. Send it as a JSON array of "+
			"entity slugs — [\"token-store\", \"auth-service\"] — or [] to clear the refs.", cc.n())
	}
	cc.after["entity_refs"] = anyList(slugs)
	if len(slugs) == 0 {
		return nil
	}

	known, err := c.loadEntities(ctx)
	if err != nil {
		return err
	}
	index := make(map[string]bool, len(known))
	for _, s := range known {
		index[s] = true
	}
	var unknown []string
	for _, s := range slugs {
		if !index[s] {
			unknown = append(unknown, strconv.Quote(s))
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return refuse("change %d refers to %s, which %s not %s in this project. The entities are: %s. "+
		"An entity ref points at architecture that already exists — to make new architecture, "+
		"raise a `propose_diagram_change` alongside this one.",
		cc.n(), strings.Join(unknown, ", "), isAre(len(unknown)), thingOrThings(len(unknown)),
		orNone(strings.Join(known, ", ")))
}

// dependencies checks the work item refs on both sides of the edge.
//
// Unknown keys inside the object are refused rather than ignored. The applier
// reads `blocks` and `relates` and nothing else, so a proposal saying
// `blocked_by` would apply cleanly, report success, and wire nothing — the
// failure mode a reviewer has no way to see.
func (c *planChecker) dependencies(ctx context.Context, cc changeCtx) error {
	obj, ok := cc.after["dependencies"].(map[string]any)
	if !ok {
		return refuse("change %d sets dependencies to something that is not an object. Send "+
			"{\"blocks\": [\"ZP-4\"], \"relates\": [\"ZP-9\"]} — refs of items that already exist. "+
			"Send one key to set that kind and leave the other alone, or [] to clear it.", cc.n())
	}
	for _, key := range sortedKeys(obj) {
		if key != "blocks" && key != "relates" {
			return refuse("change %d sets `dependencies.%s`, which is not a kind of dependency. "+
				"The kinds are `blocks` — the source must finish before the target can proceed — and `relates`, "+
				"which is worth reading together and implies no order.%s",
				cc.n(), key, reversalHint(key, cc.ref, obj[key]))
		}
		refs, isList := coerceList(obj[key])
		if !isList {
			return refuse("change %d sets dependencies.%s to something that is not a list. "+
				"Send an array of work item refs, such as [\"ZP-4\", \"ZP-9\"].", cc.n(), key)
		}
		obj[key] = anyList(refs)
		for _, target := range refs {
			if _, err := uuid.Parse(target); err == nil {
				continue
			}
			if fold(target) == fold(cc.ref) {
				return refuse("change %d has %s depending on itself. A dependency is an edge between two "+
					"different work items; whatever %s is waiting on, it is not %s.", cc.n(), cc.ref, cc.ref, cc.ref)
			}
			if _, err := c.s.item(ctx, target); err != nil {
				return err
			}
		}
	}
	return nil
}

// reversalHint answers the question the model was actually asking.
//
// `blocked_by` is the natural way to say it and there is no such edge: `blocks`
// is directed and is the only ordering there is, so the same fact is written
// from the other item. Naming both refs makes the fix a rewrite rather than a
// puzzle.
func reversalHint(key, ref string, value any) string {
	if fold(key) != "blocked_by" && fold(key) != "depends_on" && fold(key) != "blocked-by" {
		return ""
	}
	me := ref
	if me == "" {
		me = "this item"
	}
	other := "the item it waits on"
	if refs, ok := coerceList(value); ok && len(refs) > 0 {
		other = refs[0]
	}
	return fmt.Sprintf(" `blocks` is directed and is the only ordering edge there is: to say %s waits on %s, "+
		"raise the change against %s with `blocks: [%q]`.", me, other, other, me)
}

// dateOrder rejects a range that runs backwards, when the change sets both ends
// of it. A change setting only one end is checked against the stored other end
// by the write path, which is the only place that knows what it is.
func (c *planChecker) dateOrder(cc changeCtx) error {
	rawStart, hasStart := cc.after["start_date"]
	rawDue, hasDue := cc.after["due_date"]
	if !hasStart || !hasDue {
		return nil
	}
	start, err := pl.ParseDate(textOf(rawStart))
	if err != nil {
		return c.fail(cc.i, err)
	}
	due, err := pl.ParseDate(textOf(rawDue))
	if err != nil {
		return c.fail(cc.i, err)
	}
	return c.fail(cc.i, pl.ValidateDates(string(cc.target), start, due))
}

// text rejects a value that arrived as a structure where prose was asked for.
//
// A title sent as `{"en": "..."}` would be stored as Go's rendering of a map,
// which is not something anybody meant and not something anybody would notice
// until they read it in the plan.
func (c *planChecker) text(i int, field string, v any) error {
	switch v.(type) {
	case nil, string, float64, int, bool:
		return nil
	}
	return refuse("change %d sets `%s` to a %T. It is text — send it as a string.", i+1, field, v)
}

func (c *planChecker) wholeNumber(i int, field string, v any) (*int, error) {
	if v == nil {
		return nil, nil
	}
	switch t := v.(type) {
	case float64:
		n := int(t)
		return &n, nil
	case int:
		return &t, nil
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return nil, refuse("change %d sets `%s` to %q, which is not a whole number.", i+1, field, t)
		}
		return &n, nil
	}
	return nil, refuse("change %d sets `%s` to a %T, where a whole number was expected.", i+1, field, v)
}

// fail wraps a domain error as a refusal naming the change it came from.
func (c *planChecker) fail(i int, err error) error {
	if err == nil {
		return nil
	}
	return refuse("change %d: %s", i+1, strings.TrimPrefix(err.Error(), "invalid: "))
}

// ── the project's own vocabulary, read once per turn ─────────────────────────

func (c *planChecker) loadMilestones(ctx context.Context) ([]*pl.Milestone, error) {
	if c.haveMilestones {
		return c.milestones, nil
	}
	list, err := c.s.deps.Plan.ListMilestones(ctx, c.s.turn.ProjectID, c.s.principal)
	if err != nil {
		return nil, err
	}
	c.milestones, c.haveMilestones = list, true
	return list, nil
}

func (c *planChecker) loadPeople(ctx context.Context) ([]repo.MemberRow, error) {
	if c.havePeople {
		return c.people, nil
	}
	list, err := c.s.deps.members.List(ctx, c.s.deps.DB.Pool, c.s.turn.ProjectID)
	if err != nil {
		return nil, err
	}
	c.people, c.havePeople = list, true
	return list, nil
}

func (c *planChecker) loadEntities(ctx context.Context) ([]string, error) {
	if c.haveEntities {
		return c.entities, nil
	}
	list, err := c.s.deps.Diagrams.Entities(ctx, c.s.turn.ProjectID, c.s.principal)
	if err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(list))
	for _, e := range list {
		slugs = append(slugs, e.Slug)
	}
	sort.Strings(slugs)
	c.entities, c.haveEntities = slugs, true
	return slugs, nil
}

// ── shapes ───────────────────────────────────────────────────────────────────

// coerceList reads an array of strings out of whatever the model sent.
//
// Forgiving about shape and strict about meaning, which is the rule the rest of
// the argument accessors follow: `"a, b"` for an array is unambiguous and
// refusing it teaches the model nothing it needs to know about this project. A
// number or an object is not unambiguous, and gets the second return.
func coerceList(v any) ([]string, bool) {
	switch t := v.(type) {
	case nil:
		return nil, true
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if item == nil {
				continue
			}
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case []string:
		return t, true
	case string:
		var out []string
		for _, part := range strings.Split(t, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}

// anyList puts a normalised list back into `after` as the JSON shape the
// applier reads, so what is stored is what a second reader would have parsed.
func anyList(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func textOf(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func fold(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func thingOrThings(n int) string {
	if n == 1 {
		return "an entity"
	}
	return "entities"
}
