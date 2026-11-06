// Package build is the endpoint that checks the content as the build would
// (package content), without writing a bundle. `server build` writes one.
package build

import (
	"context"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/content"
)

// Report is the build report as every surface shows it.
type Report struct {
	OK       bool          `json:"ok"`
	Errors   []Issue       `json:"errors"`
	Warnings []Issue       `json:"warnings"`
	Stats    content.Stats `json:"stats"`
}

// Issue is a content.Issue with a link to what it is about.
type Issue struct {
	content.Issue
	URL string `json:"url"`
}

// Endpoint checks the content.
type Endpoint struct {
	Links app.Links
}

// model is a game's build report, and the game.
type model struct {
	GameID string
	Report content.Report
}

func (e Endpoint) Interact(ctx context.Context, store content.LoadStore, req apigen.GetBuildReportRequestObject) (model, error) {
	c, err := content.Load(ctx, store, req.GameId)
	if err != nil {
		return model{}, err
	}
	_, report := content.Compile(c)
	return model{GameID: req.GameId, Report: report}, nil
}

func (e Endpoint) Build(m model) Report {
	issues := func(in []content.Issue) []Issue {
		out := make([]Issue, len(in))
		for i, is := range in {
			out[i] = Issue{Issue: is, URL: e.Links.Target(m.GameID, is.TargetKind, is.TargetID)}
		}
		return out
	}
	r := m.Report
	return Report{OK: r.OK(), Errors: issues(r.Errors), Warnings: issues(r.Warnings), Stats: r.Stats}
}

func (Endpoint) Render(vm Report) apigen.GetBuildReportResponseObject {
	issues := func(in []Issue) []apigen.BuildIssue {
		out := make([]apigen.BuildIssue, len(in))
		for i, is := range in {
			out[i] = apigen.BuildIssue{Code: is.Code, Message: is.Message, TargetKind: is.TargetKind, TargetId: is.TargetID, Url: is.URL}
		}
		return out
	}
	s := vm.Stats
	return apigen.GetBuildReport200JSONResponse{
		Ok:       vm.OK,
		Errors:   issues(vm.Errors),
		Warnings: issues(vm.Warnings),
		Stats: apigen.BuildStats{
			Characters: s.Characters, Conversations: s.Conversations, Nodes: s.Nodes,
			Choices: s.Choices, Flags: s.Flags, Skills: s.Skills, Quests: s.Quests,
			Words: s.Words, OpenNotes: s.OpenNotes,
		},
	}
}
