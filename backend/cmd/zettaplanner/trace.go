package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	th "github.com/Felodynn/ZettaPlanner/backend/internal/domain/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// cmdTrace replays one turn.
//
// The Phase 6 exit criterion, and the tool every "why did the agent do that"
// question ends at. Messages carry the conversation; the trace carries what
// happened between messages — the model call that came back unparseable and was
// retried, the tool that was refused, the limit that tripped. A turn whose
// behaviour cannot be reconstructed is a turn whose behaviour cannot be fixed.
func cmdTrace(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("trace", flag.ContinueOnError)
	verbose := fs.Bool("v", false, "print every tool argument and answer in full")
	asJSON := fs.Bool("json", false, "emit the trace as JSON instead of prose")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("trace needs one turn id: zettaplanner trace <turn_id>")
	}
	turnID, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("%q is not a turn id", fs.Arg(0))
	}

	cfg, _, err := boot()
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx = telemetry.WithRequestID(ctx, telemetry.NewRequestID())

	svc := thread.New(db, slog.New(slog.DiscardHandler), nil, nil)
	principal, err := resolvePrincipal(ctx, db, "")
	if err != nil {
		return err
	}

	turn, events, messages, err := svc.Trace(ctx, turnID, principal)
	if err != nil {
		return err
	}

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"turn": turn, "events": events, "messages": messages,
		})
	}
	printTrace(turn, events, messages, *verbose)
	return nil
}

func printTrace(turn *th.Turn, events []repo.TurnEvent, messages []*th.Message, verbose bool) {
	fmt.Printf("turn      %s\n", turn.ID)
	fmt.Printf("project   %s   thread %s\n", turn.ProjectID, turn.ThreadID)
	fmt.Printf("asked by  %s\n", orDash(turn.RequestedByName))
	fmt.Printf("model     %s\n", orDash(turn.Model))
	fmt.Printf("status    %s", turn.Status)
	if turn.Finish != "" {
		fmt.Printf("  (%s: %s)", turn.Finish, th.FinishMeaning(turn.Finish))
	}
	fmt.Println()
	if turn.ErrorClass != "" {
		fmt.Printf("error     %s%s\n", turn.ErrorClass, detailSuffix(turn.ErrorDetail))
	}
	fmt.Printf("cost      %d in / %d out tokens · %d steps · %d retries · %s\n",
		turn.TokensIn, turn.TokensOut, turn.Steps, turn.Retries,
		time.Duration(turn.WallMS)*time.Millisecond)

	if !turn.Finish.Succeeded() && turn.Finish != "" {
		fmt.Printf("\n⚠ This turn did not succeed. `%s` means: %s\n",
			turn.Finish, th.FinishMeaning(turn.Finish))
	}

	fmt.Printf("\n── prompt ──────────────────────────────────────────────────────\n")
	for _, m := range messages {
		if m.Role == th.RoleUser {
			fmt.Printf("%s\n", strings.TrimSpace(m.Content))
		}
	}

	fmt.Printf("\n── what happened ───────────────────────────────────────────────\n")
	if len(events) == 0 {
		fmt.Println("(no trace events; this turn predates the trace or never started)")
	}
	for _, e := range events {
		printEvent(e, verbose)
	}

	fmt.Printf("\n── what it said ────────────────────────────────────────────────\n")
	for _, m := range messages {
		if m.Role == th.RoleUser {
			continue
		}
		label := string(m.Role)
		if len(m.ToolCalls) > 0 {
			var names []string
			for _, c := range m.ToolCalls {
				name := c.Name
				if c.Outcome != "" && c.Outcome != "ok" {
					name += " [" + c.Outcome + "]"
				}
				names = append(names, name)
			}
			label += " → " + strings.Join(names, ", ")
		}
		body := strings.TrimSpace(m.Content)
		if body == "" {
			fmt.Printf("[%s]\n", label)
			continue
		}
		if !verbose {
			body = clip(body, 600)
		}
		fmt.Printf("[%s]\n%s\n\n", label, indent(body, "  "))
	}
}

func printEvent(e repo.TurnEvent, verbose bool) {
	stamp := e.At.Format("15:04:05.000")
	line := fmt.Sprintf("%s  %-14s %s", stamp, e.Kind, e.Subject)
	if e.DurationMS > 0 {
		line += fmt.Sprintf("  %dms", e.DurationMS)
	}
	if e.TokensIn > 0 || e.TokensOut > 0 {
		line += fmt.Sprintf("  %d/%d tok", e.TokensIn, e.TokensOut)
	}
	if e.ErrorClass != "" {
		line += "  class=" + e.ErrorClass
	}
	fmt.Println(line)

	if len(e.Detail) == 0 {
		return
	}
	raw, err := json.Marshal(e.Detail)
	if err != nil {
		return
	}
	body := string(raw)
	if !verbose {
		body = clip(body, 240)
	}
	fmt.Printf("               %s\n", body)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… [-v for all of it]"
}

func indent(s, with string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = with + l
	}
	return strings.Join(lines, "\n")
}

func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return " — " + detail
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
