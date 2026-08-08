package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
)

// ── wire types ───────────────────────────────────────────────────────────────

// MilestoneSummary is a milestone as the API presents one.
type MilestoneSummary struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Intent    string    `json:"intent,omitempty"`
	Seq       int       `json:"seq"`
	StartDate string    `json:"start_date,omitempty" doc:"YYYY-MM-DD; absent when unset"`
	DueDate   string    `json:"due_date,omitempty" doc:"YYYY-MM-DD; absent when unset"`
	Status    string    `json:"status" enum:"planned,active,done,dropped"`
	// ItemCount and OpenCount are computed, never stored.
	ItemCount  int    `json:"item_count"`
	OpenCount  int    `json:"open_count"`
	Provenance string `json:"provenance"`
}

// WorkItemSummary is a work item without its children.
type WorkItemSummary struct {
	ID          uuid.UUID `json:"id"`
	Ref         string    `json:"ref" doc:"The human-facing handle, such as ZP-14. Issued once and never reissued"`
	Title       string    `json:"title"`
	Intent      string    `json:"intent,omitempty"`
	Acceptance  string    `json:"acceptance_criteria,omitempty"`
	Status      string    `json:"status" enum:"todo,in_progress,blocked,review,done,dropped"`
	ProgressPct int       `json:"progress_pct"`
	Priority    string    `json:"priority,omitempty" enum:"low,normal,high,urgent" doc:"Absent means nobody has triaged it, which is not the same as normal"`
	AssigneeID  string    `json:"assignee_id,omitempty"`
	MilestoneID string    `json:"milestone_id,omitempty"`
	StartDate   string    `json:"start_date,omitempty"`
	DueDate     string    `json:"due_date,omitempty"`
	Provenance  string    `json:"provenance"`
	DeletedAt   string    `json:"deleted_at,omitempty" doc:"Set when the item has been deleted. Deletion is soft, and a deleted item stays readable so its history keeps resolving"`
}

// SubTaskSummary is one leaf under a work item.
type SubTaskSummary struct {
	ID         uuid.UUID `json:"id"`
	Title      string    `json:"title"`
	Status     string    `json:"status" enum:"todo,in_progress,blocked,review,done,dropped"`
	Seq        int       `json:"seq"`
	AssigneeID string    `json:"assignee_id,omitempty"`
}

// ChecklistItemSummary is one box.
type ChecklistItemSummary struct {
	ID        uuid.UUID `json:"id"`
	Text      string    `json:"text"`
	Checked   bool      `json:"checked"`
	Seq       int       `json:"seq"`
	CheckedAt string    `json:"checked_at,omitempty" doc:"Kept after an un-tick: who ticked it and when survives somebody clearing the box"`
}

// DependencySummary is one edge, named from the other end.
type DependencySummary struct {
	ItemID uuid.UUID `json:"item_id"`
	Ref    string    `json:"ref"`
	Title  string    `json:"title"`
	Kind   string    `json:"kind" enum:"blocks,relates"`
}

// EntityRefSummary is one soft reference into the architecture graph.
type EntityRefSummary struct {
	EntityID uuid.UUID `json:"entity_id"`
	Slug     string    `json:"slug"`
	Name     string    `json:"name"`
	Relation string    `json:"relation" enum:"implements"`
}

// WorkItemDetail is one item with everything hanging off it.
type WorkItemDetail struct {
	WorkItemSummary
	AssigneeName  string                 `json:"assignee_name,omitempty"`
	MilestoneName string                 `json:"milestone_name,omitempty"`
	SubTasks      []SubTaskSummary       `json:"sub_tasks"`
	Checklist     []ChecklistItemSummary `json:"checklist"`
	Blocks        []DependencySummary    `json:"blocks" doc:"Items this one must finish before"`
	BlockedBy     []DependencySummary    `json:"blocked_by"`
	Relates       []DependencySummary    `json:"relates"`
	Implements    []EntityRefSummary     `json:"implements" doc:"Set explicitly, never derived (§3.5)"`
}

// WorklogSummary is one log entry.
type WorklogSummary struct {
	ID        uuid.UUID `json:"id"`
	ItemRef   string    `json:"item_ref,omitempty"`
	Kind      string    `json:"kind" enum:"progress,deviation,note"`
	Body      string    `json:"body"`
	ActorName string    `json:"actor_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// DeviationSummary is one recorded divergence.
type DeviationSummary struct {
	ID         uuid.UUID `json:"id"`
	ItemRef    string    `json:"item_ref,omitempty"`
	Summary    string    `json:"summary"`
	Severity   string    `json:"severity" enum:"low,medium,high"`
	Resolution string    `json:"resolution,omitempty"`
	Resolved   bool      `json:"resolved"`
	CreatedAt  time.Time `json:"created_at"`
	ResolvedAt string    `json:"resolved_at,omitempty"`
}

// WarningSummary is one computed date violation.
type WarningSummary struct {
	Kind        string    `json:"kind" enum:"blocker_date,milestone_date"`
	ItemID      uuid.UUID `json:"item_id"`
	ItemRef     string    `json:"item_ref"`
	ItemTitle   string    `json:"item_title"`
	ItemDate    string    `json:"item_date,omitempty"`
	RelatedKind string    `json:"related_kind" enum:"work_item,milestone"`
	RelatedID   uuid.UUID `json:"related_id"`
	RelatedRef  string    `json:"related_ref,omitempty"`
	RelatedName string    `json:"related_name"`
	RelatedDate string    `json:"related_date,omitempty"`
	Days        int       `json:"days" doc:"How far the violation runs, always positive"`
	Message     string    `json:"message"`
}

// ── patch bodies ─────────────────────────────────────────────────────────────
//
// Every field is a pointer: nil means absent, non-nil means write this. That is
// what policy.Fields reads to build the set the ownership table is asked about,
// and it is why no handler below names a field when deciding.
//
// The wire names must match CONTRACT §4.2 exactly. A field here that the table
// has never heard of is a field written with no owner checked; the test in
// plan_patch_test.go is what makes that impossible quietly.
//
// The optional-id and date fields carry strings rather than typed values so
// that "" can mean "clear this". A JSON null cannot say it: an absent field and
// an explicit null both decode into a nil pointer.

type milestonePatchBody struct {
	Name      *string `json:"name,omitempty"`
	Intent    *string `json:"intent,omitempty"`
	StartDate *string `json:"start_date,omitempty" doc:"YYYY-MM-DD, or \"\" to clear"`
	DueDate   *string `json:"due_date,omitempty" doc:"YYYY-MM-DD, or \"\" to clear"`
	Status    *string `json:"status,omitempty" enum:"planned,active,done,dropped"`
	Seq       *int    `json:"seq,omitempty"`
}

type workItemPatchBody struct {
	Title       *string `json:"title,omitempty"`
	Intent      *string `json:"intent,omitempty"`
	Acceptance  *string `json:"acceptance_criteria,omitempty"`
	Priority    *string `json:"priority,omitempty" enum:"low,normal,high,urgent"`
	StartDate   *string `json:"start_date,omitempty" doc:"YYYY-MM-DD, or \"\" to clear"`
	DueDate     *string `json:"due_date,omitempty" doc:"YYYY-MM-DD, or \"\" to clear"`
	AssigneeID  *string `json:"assignee_id,omitempty" doc:"An id, or \"\" to unassign"`
	MilestoneID *string `json:"milestone_id,omitempty" doc:"An id, or \"\" to detach"`

	// Dev-owned, and only on items assigned to the caller. A manager patching
	// either of these is refused: ownership is two columns, not two tiers, and
	// status is the developer's report of reality.
	Status      *string `json:"status,omitempty" enum:"todo,in_progress,blocked,review,done,dropped"`
	ProgressPct *int    `json:"progress_pct,omitempty" minimum:"0" maximum:"100"`
}

type subTaskPatchBody struct {
	Title      *string `json:"title,omitempty"`
	Status     *string `json:"status,omitempty" enum:"todo,in_progress,blocked,review,done,dropped"`
	AssigneeID *string `json:"assignee_id,omitempty"`
	Seq        *int    `json:"seq,omitempty"`
}

type checklistPatchBody struct {
	Text    *string `json:"text,omitempty"`
	Checked *bool   `json:"checked,omitempty"`
}

type deviationPatchBody struct {
	Summary    *string `json:"summary,omitempty" doc:"Reporter-owned"`
	Severity   *string `json:"severity,omitempty" enum:"low,medium,high" doc:"Reporter-owned"`
	Resolution *string `json:"resolution,omitempty" doc:"Manager-owned. Setting it resolves the deviation; clearing it reopens"`
}

// ── outputs ──────────────────────────────────────────────────────────────────

type milestoneListOutput struct {
	Body struct {
		Milestones []MilestoneSummary `json:"milestones"`
	}
}

type milestoneOutput struct{ Body MilestoneSummary }

type workItemListOutput struct {
	Body struct {
		WorkItems []WorkItemSummary `json:"work_items"`
	}
}

type workItemOutput struct{ Body WorkItemDetail }

type subTaskOutput struct{ Body SubTaskSummary }

type checklistOutput struct{ Body ChecklistItemSummary }

type dependenciesOutput struct {
	Body struct {
		Blocks  []DependencySummary `json:"blocks"`
		Relates []DependencySummary `json:"relates"`
	}
}

type entityRefsOutput struct {
	Body struct {
		Implements []EntityRefSummary `json:"implements"`
	}
}

type worklogOutput struct{ Body WorklogSummary }

type worklogListOutput struct {
	Body struct {
		Worklogs []WorklogSummary `json:"worklogs"`
	}
}

type deviationOutput struct{ Body DeviationSummary }

type deviationListOutput struct {
	Body struct {
		Deviations []DeviationSummary `json:"deviations"`
	}
}

type warningListOutput struct {
	Body struct {
		Warnings []WarningSummary `json:"warnings"`
	}
}

// ── registration ─────────────────────────────────────────────────────────────

func (s *Server) registerPlan() {
	s.registerMilestones()
	s.registerWorkItems()
	s.registerItemChildren()
	s.registerWorklogsAndDeviations()

	huma.Register(s.API, huma.Operation{
		OperationID: "list-warnings",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/warnings",
		Summary:     "Dependency and date violations",
		Description: "Computed on every call and stored nowhere: a stored warning is stale the moment a date " +
			"moves, and reads identically whether it is true now or was true in April. " +
			"Nothing is rescheduled — a cascade that is right 90% of the time destroys the dates somebody set by hand.",
		Tags: []string{"plan"},
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
	}) (*warningListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Plan.Warnings(ctx, in.PID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &warningListOutput{}
		out.Body.Warnings = []WarningSummary{}
		for _, w := range list {
			out.Body.Warnings = append(out.Body.Warnings, warningSummary(w))
		}
		return out, nil
	})
}

func (s *Server) registerMilestones() {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-milestones",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/milestones",
		Summary:     "A project's milestones",
		Tags:        []string{"plan"},
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
	}) (*milestoneListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Plan.ListMilestones(ctx, in.PID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		counts, err := s.deps.Plan.ItemCounts(ctx, s.deps.DB.Pool, in.PID)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &milestoneListOutput{}
		out.Body.Milestones = []MilestoneSummary{}
		for _, m := range list {
			out.Body.Milestones = append(out.Body.Milestones, milestoneSummary(m, counts[m.ID]))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "create-milestone",
		Method:        http.MethodPost,
		Path:          "/api/v1/projects/{pid}/milestones",
		Summary:       "Create a milestone",
		Description:   "Manager-owned (CONTRACT §4.2). Seq defaults to one past the last milestone.",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		PID  uuid.UUID `path:"pid"`
		Body struct {
			Name      string `json:"name" minLength:"1"`
			Intent    string `json:"intent,omitempty"`
			StartDate string `json:"start_date,omitempty"`
			DueDate   string `json:"due_date,omitempty"`
			Status    string `json:"status,omitempty" enum:"planned,active,done,dropped"`
			Seq       *int   `json:"seq,omitempty"`
		}
	}) (*milestoneOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		m, err := s.deps.Plan.CreateMilestone(ctx, plansvc.CreateMilestoneInput{
			ProjectID: in.PID,
			Name:      in.Body.Name,
			Intent:    in.Body.Intent,
			StartDate: in.Body.StartDate,
			DueDate:   in.Body.DueDate,
			Status:    in.Body.Status,
			Seq:       in.Body.Seq,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &milestoneOutput{Body: milestoneSummary(m, [2]int{})}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "update-milestone",
		Method:      http.MethodPatch,
		Path:        "/api/v1/milestones/{id}",
		Summary:     "Change a milestone",
		Description: "Every field in the body is resolved to its owning role before anything is written. " +
			"A status move is recorded as milestone.status_changed, not as a generic update.",
		Tags: []string{"plan"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body milestonePatchBody
	}) (*milestoneOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		fields := policy.Fields(in.Body)
		if len(fields) == 0 {
			return nil, s.mapErr(ctx, ValidationFailed(
				"The request body set no fields. Send at least one of %v.",
				policy.PatchFieldNames(milestonePatchBody{})))
		}
		m, err := s.deps.Plan.UpdateMilestone(ctx, in.ID, plansvc.UpdateMilestoneInput{
			Fields:    fields,
			Name:      in.Body.Name,
			Intent:    in.Body.Intent,
			StartDate: in.Body.StartDate,
			DueDate:   in.Body.DueDate,
			Status:    in.Body.Status,
			Seq:       in.Body.Seq,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &milestoneOutput{Body: milestoneSummary(m, [2]int{})}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "delete-milestone",
		Method:        http.MethodDelete,
		Path:          "/api/v1/milestones/{id}",
		Summary:       "Delete a milestone",
		Description:   "Soft. The work items under it survive and lose the pointer: deleting a grouping must not delete the work.",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.deps.Plan.DeleteMilestone(ctx, in.ID, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})
}

func (s *Server) registerWorkItems() {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-work-items",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/work-items",
		Summary:     "A project's work items",
		Description: "Filterable by assignee, status and milestone. `milestone=none` selects the items in no milestone.",
		Tags:        []string{"plan"},
	}, func(ctx context.Context, in *struct {
		PID       uuid.UUID `path:"pid"`
		Assignee  string    `query:"assignee" doc:"A user id"`
		Status    string    `query:"status" enum:"todo,in_progress,blocked,review,done,dropped"`
		Milestone string    `query:"milestone" doc:"A milestone id, or the literal none"`
	}) (*workItemListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}

		f := repo.WorkItemFilter{Status: pl.Status(in.Status)}
		if in.Assignee != "" {
			id, err := uuid.Parse(in.Assignee)
			if err != nil {
				return nil, s.mapErr(ctx, ValidationFailed("assignee=%q is not a user id.", in.Assignee))
			}
			f.AssigneeID = &id
		}
		switch {
		case in.Milestone == "none":
			f.Unassigned = true
		case in.Milestone != "":
			id, err := uuid.Parse(in.Milestone)
			if err != nil {
				return nil, s.mapErr(ctx, ValidationFailed(
					"milestone=%q is neither a milestone id nor the literal `none`.", in.Milestone))
			}
			f.MilestoneID = &id
		}

		list, err := s.deps.Plan.ListWorkItems(ctx, in.PID, f, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &workItemListOutput{}
		out.Body.WorkItems = []WorkItemSummary{}
		for _, w := range list {
			out.Body.WorkItems = append(out.Body.WorkItems, workItemSummary(w))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "create-work-item",
		Method:        http.MethodPost,
		Path:          "/api/v1/projects/{pid}/work-items",
		Summary:       "Create a work item",
		Description:   "Manager-owned. The ref is issued by the server from a monotonic counter and is never reissued.",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		PID  uuid.UUID `path:"pid"`
		Body struct {
			Title       string `json:"title" minLength:"1"`
			Intent      string `json:"intent,omitempty"`
			Acceptance  string `json:"acceptance_criteria,omitempty"`
			MilestoneID string `json:"milestone_id,omitempty"`
			AssigneeID  string `json:"assignee_id,omitempty"`
			Priority    string `json:"priority,omitempty" enum:"low,normal,high,urgent"`
			Status      string `json:"status,omitempty" enum:"todo,in_progress,blocked,review,done,dropped"`
			StartDate   string `json:"start_date,omitempty"`
			DueDate     string `json:"due_date,omitempty"`
		}
	}) (*workItemOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		w, err := s.deps.Plan.CreateWorkItem(ctx, plansvc.CreateWorkItemInput{
			ProjectID:   in.PID,
			Title:       in.Body.Title,
			Intent:      in.Body.Intent,
			Acceptance:  in.Body.Acceptance,
			MilestoneID: in.Body.MilestoneID,
			AssigneeID:  in.Body.AssigneeID,
			Priority:    in.Body.Priority,
			Status:      in.Body.Status,
			StartDate:   in.Body.StartDate,
			DueDate:     in.Body.DueDate,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &workItemOutput{Body: WorkItemDetail{
			WorkItemSummary: workItemSummary(w),
			SubTasks:        []SubTaskSummary{},
			Checklist:       []ChecklistItemSummary{},
			Blocks:          []DependencySummary{},
			BlockedBy:       []DependencySummary{},
			Relates:         []DependencySummary{},
			Implements:      []EntityRefSummary{},
		}}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-work-item",
		Method:      http.MethodGet,
		Path:        "/api/v1/work-items/{id}",
		Summary:     "One work item with its subtasks, checklist, dependencies and entity refs",
		Description: "A deleted item is still returned, carrying deleted_at. Deletion is soft and the ref is never " +
			"reissued, so every worklog and audit entry naming it still resolves.",
		Tags: []string{"plan"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*workItemOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		d, err := s.deps.Plan.GetWorkItem(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &workItemOutput{Body: workItemDetail(d)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "update-work-item",
		Method:      http.MethodPatch,
		Path:        "/api/v1/work-items/{id}",
		Summary:     "Change a work item",
		Description: "Field ownership is enforced per field (CONTRACT §4.2). A developer may write status and " +
			"progress_pct on items assigned to them; everything else is manager-owned and a developer's route " +
			"to it is a proposal. A manager may not write status either — it is the developer's report of reality.",
		Tags: []string{"plan"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body workItemPatchBody
	}) (*workItemOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		fields := policy.Fields(in.Body)
		if len(fields) == 0 {
			return nil, s.mapErr(ctx, ValidationFailed(
				"The request body set no fields. Send at least one of %v.",
				policy.PatchFieldNames(workItemPatchBody{})))
		}
		d, err := s.deps.Plan.UpdateWorkItem(ctx, in.ID, plansvc.UpdateWorkItemInput{
			Fields:      fields,
			Title:       in.Body.Title,
			Intent:      in.Body.Intent,
			Acceptance:  in.Body.Acceptance,
			Status:      in.Body.Status,
			ProgressPct: in.Body.ProgressPct,
			Priority:    in.Body.Priority,
			AssigneeID:  in.Body.AssigneeID,
			MilestoneID: in.Body.MilestoneID,
			StartDate:   in.Body.StartDate,
			DueDate:     in.Body.DueDate,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &workItemOutput{Body: workItemDetail(d)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "delete-work-item",
		Method:        http.MethodDelete,
		Path:          "/api/v1/work-items/{id}",
		Summary:       "Delete a work item",
		Description:   "Soft. Its subtasks, checklist, worklogs and dependencies stay, and its ref is never reissued.",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.deps.Plan.DeleteWorkItem(ctx, in.ID, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})
}

func (s *Server) registerItemChildren() {
	huma.Register(s.API, huma.Operation{
		OperationID:   "create-sub-task",
		Method:        http.MethodPost,
		Path:          "/api/v1/work-items/{id}/sub-tasks",
		Summary:       "Add a subtask",
		Description:   "Dev-owned, scoped to the assignee of the parent item (§4.2: within own item).",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			Title      string `json:"title" minLength:"1"`
			Status     string `json:"status,omitempty" enum:"todo,in_progress,blocked,review,done,dropped"`
			AssigneeID string `json:"assignee_id,omitempty"`
			Seq        *int   `json:"seq,omitempty"`
		}
	}) (*subTaskOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		st, err := s.deps.Plan.CreateSubTask(ctx, plansvc.CreateSubTaskInput{
			WorkItemID: in.ID,
			Title:      in.Body.Title,
			Status:     in.Body.Status,
			AssigneeID: in.Body.AssigneeID,
			Seq:        in.Body.Seq,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &subTaskOutput{Body: subTaskSummary(st)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "update-sub-task",
		Method:      http.MethodPatch,
		Path:        "/api/v1/sub-tasks/{id}",
		Summary:     "Change a subtask",
		Tags:        []string{"plan"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body subTaskPatchBody
	}) (*subTaskOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		fields := policy.Fields(in.Body)
		if len(fields) == 0 {
			return nil, s.mapErr(ctx, ValidationFailed(
				"The request body set no fields. Send at least one of %v.",
				policy.PatchFieldNames(subTaskPatchBody{})))
		}
		st, err := s.deps.Plan.UpdateSubTask(ctx, in.ID, plansvc.UpdateSubTaskInput{
			Fields:     fields,
			Title:      in.Body.Title,
			Status:     in.Body.Status,
			AssigneeID: in.Body.AssigneeID,
			Seq:        in.Body.Seq,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &subTaskOutput{Body: subTaskSummary(st)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "delete-sub-task",
		Method:        http.MethodDelete,
		Path:          "/api/v1/sub-tasks/{id}",
		Summary:       "Delete a subtask",
		Description:   "Soft.",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.deps.Plan.DeleteSubTask(ctx, in.ID, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "set-dependencies",
		Method:      http.MethodPut,
		Path:        "/api/v1/work-items/{id}/dependencies",
		Summary:     "Replace an item's dependencies",
		Description: "Manager-owned. Cycles in `blocks` are rejected at write time with the offending path named. " +
			"An omitted key leaves that kind alone; an empty array clears it.",
		Tags: []string{"plan"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			Blocks  *[]uuid.UUID `json:"blocks,omitempty" doc:"Items this one must finish before"`
			Relates *[]uuid.UUID `json:"relates,omitempty" doc:"Worth reading together; no ordering, so cycles are fine"`
		}
	}) (*dependenciesOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if in.Body.Blocks == nil && in.Body.Relates == nil {
			return nil, s.mapErr(ctx, ValidationFailed(
				"The request body set neither `blocks` nor `relates`. Send at least one; an empty array clears that kind."))
		}
		list, err := s.deps.Plan.SetDependencies(ctx, in.ID, plansvc.SetDependenciesInput{
			Blocks:  in.Body.Blocks,
			Relates: in.Body.Relates,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &dependenciesOutput{}
		out.Body.Blocks, out.Body.Relates = []DependencySummary{}, []DependencySummary{}
		for _, d := range list {
			if d.Kind == pl.DepBlocks {
				out.Body.Blocks = append(out.Body.Blocks, dependencyTo(d))
			} else {
				out.Body.Relates = append(out.Body.Relates, dependencyTo(d))
			}
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "set-entity-refs",
		Method:      http.MethodPut,
		Path:        "/api/v1/work-items/{id}/entity-refs",
		Summary:     "Replace an item's references to the architecture",
		Description: "Set explicitly and never derived (§3.5). They exist so context assembly can pull the " +
			"relevant architecture slice when the item is discussed.",
		Tags: []string{"plan"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			Implements []uuid.UUID `json:"implements"`
		}
	}) (*entityRefsOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Plan.SetEntityRefs(ctx, in.ID, in.Body.Implements, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &entityRefsOutput{}
		out.Body.Implements = []EntityRefSummary{}
		for _, r := range list {
			out.Body.Implements = append(out.Body.Implements, EntityRefSummary{
				EntityID: r.EntityID, Slug: r.EntitySlug, Name: r.EntityName, Relation: string(r.Relation),
			})
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "add-checklist-item",
		Method:        http.MethodPost,
		Path:          "/api/v1/work-items/{id}/checklist",
		Summary:       "Add a checklist item",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			Text string `json:"text" minLength:"1"`
			Seq  *int   `json:"seq,omitempty"`
		}
	}) (*checklistOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		c, err := s.deps.Plan.AddChecklistItem(ctx, in.ID, in.Body.Text, in.Body.Seq, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &checklistOutput{Body: checklistSummary(c)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "update-checklist-item",
		Method:      http.MethodPatch,
		Path:        "/api/v1/checklist/{id}",
		Summary:     "Tick, clear, or retitle a checklist item",
		Description: "Both directions are recorded as checklist.checked with before and after. A box ticked and " +
			"then cleared is a signal, and recording only the tick hides it.",
		Tags: []string{"plan"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body checklistPatchBody
	}) (*checklistOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		fields := policy.Fields(in.Body)
		if len(fields) == 0 {
			return nil, s.mapErr(ctx, ValidationFailed(
				"The request body set no fields. Send at least one of %v.",
				policy.PatchFieldNames(checklistPatchBody{})))
		}
		c, err := s.deps.Plan.UpdateChecklistItem(ctx, in.ID, plansvc.UpdateChecklistInput{
			Fields:  fields,
			Text:    in.Body.Text,
			Checked: in.Body.Checked,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &checklistOutput{Body: checklistSummary(c)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "delete-checklist-item",
		Method:        http.MethodDelete,
		Path:          "/api/v1/checklist/{id}",
		Summary:       "Delete a checklist item",
		Description:   "Soft.",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.deps.Plan.DeleteChecklistItem(ctx, in.ID, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})
}

func (s *Server) registerWorklogsAndDeviations() {
	huma.Register(s.API, huma.Operation{
		OperationID:   "add-worklog",
		Method:        http.MethodPost,
		Path:          "/api/v1/work-items/{id}/worklogs",
		Summary:       "Append a worklog to an item",
		Description:   "Anyone on the project may write one. There is no edit and no delete: correct by adding another.",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			Kind string `json:"kind,omitempty" enum:"progress,deviation,note"`
			Body string `json:"body" minLength:"1"`
		}
	}) (*worklogOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		itemID := in.ID
		w, err := s.deps.Plan.AddWorklog(ctx, &itemID, uuid.Nil, in.Body.Kind, in.Body.Body, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &worklogOutput{Body: worklogSummary(w)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "list-worklogs",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/worklogs",
		Summary:     "A project's worklogs, newest first",
		Tags:        []string{"plan"},
	}, func(ctx context.Context, in *struct {
		PID   uuid.UUID `path:"pid"`
		Item  string    `query:"item" doc:"A work item id"`
		Kind  string    `query:"kind" enum:"progress,deviation,note"`
		Since string    `query:"since" doc:"RFC 3339 instant; entries at or after it"`
		Limit int       `query:"limit" minimum:"1" maximum:"500"`
	}) (*worklogListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		f := repo.WorklogFilter{Kind: pl.WorklogKind(in.Kind), Limit: in.Limit}
		if in.Item != "" {
			id, err := uuid.Parse(in.Item)
			if err != nil {
				return nil, s.mapErr(ctx, ValidationFailed("item=%q is not a work item id.", in.Item))
			}
			f.WorkItemID = &id
		}
		if in.Since != "" {
			t, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, s.mapErr(ctx, ValidationFailed(
					"since=%q is not an RFC 3339 instant, for example 2026-08-01T00:00:00Z.", in.Since))
			}
			f.Since = &t
		}
		list, err := s.deps.Plan.ListWorklogs(ctx, in.PID, f, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &worklogListOutput{}
		out.Body.Worklogs = []WorklogSummary{}
		for _, w := range list {
			out.Body.Worklogs = append(out.Body.Worklogs, worklogSummary(w))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "raise-deviation",
		Method:        http.MethodPost,
		Path:          "/api/v1/projects/{pid}/deviations",
		Summary:       "Record a divergence between the plan and reality",
		Description:   "Anyone on the project may raise one. Supplying `body` files the worklog describing it and links the two.",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		PID  uuid.UUID `path:"pid"`
		Body struct {
			WorkItemID string `json:"work_item_id,omitempty"`
			Summary    string `json:"summary" minLength:"1"`
			Severity   string `json:"severity,omitempty" enum:"low,medium,high"`
			Body       string `json:"body,omitempty" doc:"Files a worklog alongside the deviation and links the two"`
		}
	}) (*deviationOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		d, err := s.deps.Plan.RaiseDeviation(ctx, plansvc.CreateDeviationInput{
			ProjectID:  in.PID,
			WorkItemID: in.Body.WorkItemID,
			Summary:    in.Body.Summary,
			Severity:   in.Body.Severity,
			Body:       in.Body.Body,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &deviationOutput{Body: deviationSummary(d)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "list-deviations",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/deviations",
		Summary:     "A project's deviations",
		Tags:        []string{"plan"},
	}, func(ctx context.Context, in *struct {
		PID  uuid.UUID `path:"pid"`
		Item string    `query:"item"`
		Open bool      `query:"open" doc:"Restrict to unresolved deviations"`
	}) (*deviationListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		f := repo.DeviationFilter{OpenOnly: in.Open}
		if in.Item != "" {
			id, err := uuid.Parse(in.Item)
			if err != nil {
				return nil, s.mapErr(ctx, ValidationFailed("item=%q is not a work item id.", in.Item))
			}
			f.WorkItemID = &id
		}
		list, err := s.deps.Plan.ListDeviations(ctx, in.PID, f, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &deviationListOutput{}
		out.Body.Deviations = []DeviationSummary{}
		for _, d := range list {
			out.Body.Deviations = append(out.Body.Deviations, deviationSummary(d))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "update-deviation",
		Method:      http.MethodPatch,
		Path:        "/api/v1/deviations/{id}",
		Summary:     "Change or resolve a deviation",
		Description: "Split ownership: the reporter owns summary and severity, a manager owns the resolution. " +
			"Setting a resolution resolves it; clearing one reopens it, and both are recorded as deviation.resolved.",
		Tags: []string{"plan"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body deviationPatchBody
	}) (*deviationOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		fields := policy.Fields(in.Body)
		if len(fields) == 0 {
			return nil, s.mapErr(ctx, ValidationFailed(
				"The request body set no fields. Send at least one of %v.",
				policy.PatchFieldNames(deviationPatchBody{})))
		}
		d, err := s.deps.Plan.UpdateDeviation(ctx, in.ID, plansvc.UpdateDeviationInput{
			Fields:     fields,
			Summary:    in.Body.Summary,
			Severity:   in.Body.Severity,
			Resolution: in.Body.Resolution,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &deviationOutput{Body: deviationSummary(d)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "delete-deviation",
		Method:        http.MethodDelete,
		Path:          "/api/v1/deviations/{id}",
		Summary:       "Delete a deviation",
		Description:   "Soft, and manager-owned. The worklog that describes it is never deleted.",
		Tags:          []string{"plan"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.deps.Plan.DeleteDeviation(ctx, in.ID, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})
}

// ── renderers ────────────────────────────────────────────────────────────────

func milestoneSummary(m *pl.Milestone, counts [2]int) MilestoneSummary {
	return MilestoneSummary{
		ID:         m.ID,
		Name:       m.Name,
		Intent:     m.Intent,
		Seq:        m.Seq,
		StartDate:  pl.FormatDate(m.StartDate),
		DueDate:    pl.FormatDate(m.DueDate),
		Status:     string(m.Status),
		ItemCount:  counts[0],
		OpenCount:  counts[1],
		Provenance: string(m.Provenance),
	}
}

func workItemSummary(w *pl.WorkItem) WorkItemSummary {
	out := WorkItemSummary{
		ID:          w.ID,
		Ref:         w.Ref,
		Title:       w.Title,
		Intent:      w.Intent,
		Acceptance:  w.Acceptance,
		Status:      string(w.Status),
		ProgressPct: w.ProgressPct,
		Priority:    string(w.Priority),
		StartDate:   pl.FormatDate(w.StartDate),
		DueDate:     pl.FormatDate(w.DueDate),
		Provenance:  string(w.Provenance),
	}
	if w.AssigneeID != nil {
		out.AssigneeID = w.AssigneeID.String()
	}
	if w.MilestoneID != nil {
		out.MilestoneID = w.MilestoneID.String()
	}
	if w.DeletedAt != nil {
		out.DeletedAt = w.DeletedAt.Format(time.RFC3339)
	}
	return out
}

func workItemDetail(d *plansvc.ItemDetail) WorkItemDetail {
	out := WorkItemDetail{
		WorkItemSummary: workItemSummary(d.Item),
		AssigneeName:    d.AssigneeName,
		SubTasks:        []SubTaskSummary{},
		Checklist:       []ChecklistItemSummary{},
		Blocks:          []DependencySummary{},
		BlockedBy:       []DependencySummary{},
		Relates:         []DependencySummary{},
		Implements:      []EntityRefSummary{},
	}
	if d.Milestone != nil {
		out.MilestoneName = d.Milestone.Name
	}
	for _, st := range d.SubTasks {
		out.SubTasks = append(out.SubTasks, subTaskSummary(st))
	}
	for _, c := range d.Checklist {
		out.Checklist = append(out.Checklist, checklistSummary(c))
	}
	for _, dep := range d.Blocks {
		out.Blocks = append(out.Blocks, dependencyTo(dep))
	}
	for _, dep := range d.BlockedBy {
		out.BlockedBy = append(out.BlockedBy, dependencyFrom(dep))
	}
	for _, dep := range d.Relates {
		// `relates` arrives from both directions; the other end is whichever
		// one is not this item.
		if dep.FromItemID == d.Item.ID {
			out.Relates = append(out.Relates, dependencyTo(dep))
		} else {
			out.Relates = append(out.Relates, dependencyFrom(dep))
		}
	}
	for _, r := range d.Refs {
		out.Implements = append(out.Implements, EntityRefSummary{
			EntityID: r.EntityID, Slug: r.EntitySlug, Name: r.EntityName, Relation: string(r.Relation),
		})
	}
	return out
}

func subTaskSummary(st *pl.SubTask) SubTaskSummary {
	out := SubTaskSummary{ID: st.ID, Title: st.Title, Status: string(st.Status), Seq: st.Seq}
	if st.AssigneeID != nil {
		out.AssigneeID = st.AssigneeID.String()
	}
	return out
}

func checklistSummary(c *pl.ChecklistItem) ChecklistItemSummary {
	out := ChecklistItemSummary{ID: c.ID, Text: c.Text, Checked: c.Checked, Seq: c.Seq}
	if c.CheckedAt != nil {
		out.CheckedAt = c.CheckedAt.Format(time.RFC3339)
	}
	return out
}

// dependencyTo names the far end of an outgoing edge, dependencyFrom the far
// end of an incoming one. Two functions rather than one with a flag, because
// naming the wrong end is a bug that reads as correct.
func dependencyTo(d pl.Dependency) DependencySummary {
	return DependencySummary{ItemID: d.ToItemID, Ref: d.ToRef, Title: d.ToTitle, Kind: string(d.Kind)}
}

func dependencyFrom(d pl.Dependency) DependencySummary {
	return DependencySummary{ItemID: d.FromItemID, Ref: d.FromRef, Title: d.FromTitle, Kind: string(d.Kind)}
}

func worklogSummary(w *pl.Worklog) WorklogSummary {
	return WorklogSummary{
		ID: w.ID, ItemRef: w.ItemRef, Kind: string(w.Kind), Body: w.Body,
		ActorName: w.ActorName, CreatedAt: w.CreatedAt,
	}
}

func deviationSummary(d *pl.Deviation) DeviationSummary {
	out := DeviationSummary{
		ID: d.ID, ItemRef: d.ItemRef, Summary: d.Summary, Severity: string(d.Severity),
		Resolution: d.Resolution, Resolved: d.Resolved(), CreatedAt: d.CreatedAt,
	}
	if d.ResolvedAt != nil {
		out.ResolvedAt = d.ResolvedAt.Format(time.RFC3339)
	}
	return out
}

func warningSummary(w pl.Warning) WarningSummary {
	return WarningSummary{
		Kind:        string(w.Kind),
		ItemID:      w.ItemID,
		ItemRef:     w.ItemRef,
		ItemTitle:   w.ItemTitle,
		ItemDate:    pl.FormatDate(w.ItemDate),
		RelatedKind: w.RelatedKind,
		RelatedID:   w.RelatedID,
		RelatedRef:  w.RelatedRef,
		RelatedName: w.RelatedName,
		RelatedDate: pl.FormatDate(w.RelatedDate),
		Days:        w.Days,
		Message:     w.Message,
	}
}
