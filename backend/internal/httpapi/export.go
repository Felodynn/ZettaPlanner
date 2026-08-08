package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// markdownOutput carries a spec.md.
//
// A document rather than a JSON object with the markdown inside a string field:
// the whole point is that somebody saves this file and hands it to a reader who
// has no access to this system, and asking them to unwrap JSON first is an
// obstacle between the plan and the person doing the work.
type markdownOutput struct {
	ContentType string `header:"Content-Type"`
	Disposition string `header:"Content-Disposition"`
	Body        []byte
}

const exportDescription = "Bundles intent, acceptance criteria, the constraints that are actually recorded — " +
	"blockers in both directions, dates against the milestone's, open deviations, and anything unresolved or " +
	"never human-checked in the architecture it touches — and the canonical YAML slice of the referenced " +
	"entities (§6.2).\n\n" +
	"**Manual only, and one-way.** There is no trigger and nothing reads the document back in; " +
	"the Lumix handoff is deferred to v0.1 (§10). The call is a POST because it writes a " +
	"`spec.exported` audit entry: who took this outside and when is a question a retrospective asks, " +
	"and it cannot be answered later if nobody recorded it."

func (s *Server) registerExports() {
	huma.Register(s.API, huma.Operation{
		OperationID: "export-work-item-spec",
		Method:      http.MethodPost,
		Path:        "/api/v1/work-items/{id}/spec-export",
		Summary:     "Export a work item as spec.md",
		Description: exportDescription,
		Tags:        []string{"export"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*markdownOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		doc, err := s.deps.Export.WorkItem(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return markdownDoc(doc.Filename, doc.Body), nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "export-milestone-spec",
		Method:      http.MethodPost,
		Path:        "/api/v1/milestones/{id}/spec-export",
		Summary:     "Export a milestone as spec.md",
		Description: exportDescription + "\n\nThe milestone form carries every work item under it, " +
			"and its constraints name only the dependencies that leave the milestone — one inside it is " +
			"already visible in the item list, and repeating it buries the ones that are somebody else's.",
		Tags: []string{"export"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*markdownOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		doc, err := s.deps.Export.Milestone(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return markdownDoc(doc.Filename, doc.Body), nil
	})
}

func markdownDoc(filename string, body []byte) *markdownOutput {
	return &markdownOutput{
		ContentType: "text/markdown; charset=utf-8",
		Disposition: `attachment; filename="` + filename + `"`,
		Body:        body,
	}
}
