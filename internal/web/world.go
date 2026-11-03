package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	"github.com/KevinMcHugh/rpg/internal/content"
	"github.com/KevinMcHugh/rpg/internal/game"
	"github.com/KevinMcHugh/rpg/internal/game/play"
	"github.com/KevinMcHugh/rpg/internal/script"
	"github.com/KevinMcHugh/rpg/internal/server"
	"github.com/KevinMcHugh/rpg/internal/server/world"
)

// ---- the world page ---------------------------------------------------------

func (s *Server) worldPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	flags, err := server.BuildViewModel(ctx, world.ListFlagsEndpoint{Store: s.Store, Links: s.Links}, apigen.ListFlagsRequestObject{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	skills, err := server.BuildViewModel(ctx, world.ListSkillsEndpoint{Store: s.Store, Links: s.Links}, apigen.ListSkillsRequestObject{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	quests, err := server.BuildViewModel(ctx, world.ListQuestsEndpoint{Store: s.Store, Links: s.Links}, apigen.ListQuestsRequestObject{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byTarget, err := s.allNotes(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "world", map[string]any{"Flags": flags, "Skills": skills, "Quests": quests, "Notes": byTarget})
}

func (s *Server) createFlag(w http.ResponseWriter, r *http.Request) {
	_, err := server.BuildViewModel(r.Context(), world.CreateFlagEndpoint{Store: s.Store, Links: s.Links}, apigen.CreateFlagRequestObject{
		Body: &apigen.FlagCreate{Key: r.FormValue("key"), Description: r.FormValue("description"), InitialValue: formInt(r, "initial_value")},
	})
	s.after(w, r, err, "/world#flags", "Saved.")
}

func (s *Server) updateFlag(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = r.ParseForm()
	_, err := server.BuildViewModel(r.Context(), world.UpdateFlagEndpoint{Store: s.Store, Links: s.Links}, apigen.UpdateFlagRequestObject{
		FlagId: id,
		Body:   &apigen.FlagUpdate{Key: formPtr(r, "key"), Description: formPtr(r, "description"), InitialValue: formIntPtr(r, "initial_value")},
	})
	s.after(w, r, err, "/world#flag-"+id, "Saved.")
}

func (s *Server) deleteFlag(w http.ResponseWriter, r *http.Request) {
	_, err := server.BuildViewModel(r.Context(), world.DeleteFlagEndpoint{Store: s.Store}, apigen.DeleteFlagRequestObject{FlagId: chi.URLParam(r, "id")})
	s.after(w, r, err, "/world#flags", "Deleted.")
}

func (s *Server) createSkill(w http.ResponseWriter, r *http.Request) {
	_, err := server.BuildViewModel(r.Context(), world.CreateSkillEndpoint{Store: s.Store, Links: s.Links}, apigen.CreateSkillRequestObject{
		Body: &apigen.SkillCreate{Name: r.FormValue("name"), Key: r.FormValue("key"), InitialLevel: formInt(r, "initial_level")},
	})
	s.after(w, r, err, "/world#skills", "Saved.")
}

func (s *Server) updateSkill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = r.ParseForm()
	_, err := server.BuildViewModel(r.Context(), world.UpdateSkillEndpoint{Store: s.Store, Links: s.Links}, apigen.UpdateSkillRequestObject{
		SkillId: id,
		Body:    &apigen.SkillUpdate{Name: formPtr(r, "name"), Key: formPtr(r, "key"), InitialLevel: formIntPtr(r, "initial_level")},
	})
	s.after(w, r, err, "/world#skill-"+id, "Saved.")
}

func (s *Server) deleteSkill(w http.ResponseWriter, r *http.Request) {
	_, err := server.BuildViewModel(r.Context(), world.DeleteSkillEndpoint{Store: s.Store}, apigen.DeleteSkillRequestObject{SkillId: chi.URLParam(r, "id")})
	s.after(w, r, err, "/world#skills", "Deleted.")
}

// ---- quests -----------------------------------------------------------------

func (s *Server) questPage(w http.ResponseWriter, r *http.Request) {
	q, err := server.BuildViewModel(r.Context(), world.GetQuestEndpoint{Store: s.Store, Links: s.Links}, apigen.GetQuestRequestObject{QuestId: chi.URLParam(r, "id")})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byTarget, err := s.allNotes(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := 1
	for _, st := range q.Stages {
		if st.Number >= next {
			next = st.Number + 1
		}
	}
	s.render(w, r, http.StatusOK, "quest", map[string]any{"Quest": q, "Next": next, "Notes": byTarget["quest:"+q.ID]})
}

func (s *Server) stageRedirect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	st, err := s.Store.GetQuestStage(r.Context(), id)
	if err != nil {
		s.fail(w, r, app.NotFound(err, "stage", id))
		return
	}
	http.Redirect(w, r, s.Links.Quest(st.QuestID)+"#stage-"+st.ID, http.StatusSeeOther)
}

func (s *Server) createQuest(w http.ResponseWriter, r *http.Request) {
	q, err := server.BuildViewModel(r.Context(), world.CreateQuestEndpoint{Store: s.Store, Links: s.Links}, apigen.CreateQuestRequestObject{
		Body: &apigen.QuestCreate{Name: r.FormValue("name"), Key: r.FormValue("key"), Summary: r.FormValue("summary")},
	})
	if err != nil {
		s.after(w, r, err, "/world#quests", "")
		return
	}
	redirect(w, r, "/quests/"+url.PathEscape(q.ID), "Saved.", false)
}

func (s *Server) updateQuest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = r.ParseForm()
	_, err := server.BuildViewModel(r.Context(), world.UpdateQuestEndpoint{Store: s.Store, Links: s.Links}, apigen.UpdateQuestRequestObject{
		QuestId: id,
		Body:    &apigen.QuestUpdate{Name: formPtr(r, "name"), Key: formPtr(r, "key"), Summary: formPtr(r, "summary")},
	})
	s.after(w, r, err, "/quests/"+url.PathEscape(id), "Saved.")
}

func (s *Server) deleteQuest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, err := server.BuildViewModel(r.Context(), world.DeleteQuestEndpoint{Store: s.Store}, apigen.DeleteQuestRequestObject{QuestId: id})
	if err != nil {
		s.after(w, r, err, "/quests/"+url.PathEscape(id), "")
		return
	}
	redirect(w, r, "/world#quests", "Deleted.", false)
}

func (s *Server) createStage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, err := server.BuildViewModel(r.Context(), world.CreateStageEndpoint{Store: s.Store, Links: s.Links}, apigen.CreateQuestStageRequestObject{
		QuestId: id,
		Body:    &apigen.QuestStageCreate{Number: formInt(r, "number"), Journal: r.FormValue("journal"), Completes: r.FormValue("completes") != ""},
	})
	s.after(w, r, err, "/quests/"+url.PathEscape(id), "Saved.")
}

func (s *Server) updateStage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = r.ParseForm()
	q, err := server.BuildViewModel(r.Context(), world.UpdateStageEndpoint{Store: s.Store, Links: s.Links}, apigen.UpdateQuestStageRequestObject{
		StageId: id,
		Body:    &apigen.QuestStageUpdate{Number: formIntPtr(r, "number"), Journal: formPtr(r, "journal"), Completes: formBoolPtr(r, "completes")},
	})
	back := "/stages/" + url.PathEscape(id)
	if err == nil {
		back = "/quests/" + url.PathEscape(q.ID) + "#stage-" + id
	}
	s.after(w, r, err, back, "Saved.")
}

func (s *Server) deleteStage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	st, err := s.Store.GetQuestStage(r.Context(), id)
	if err != nil {
		s.fail(w, r, app.NotFound(err, "stage", id))
		return
	}
	_, err = server.BuildViewModel(r.Context(), world.DeleteStageEndpoint{Store: s.Store}, apigen.DeleteQuestStageRequestObject{StageId: id})
	s.after(w, r, err, "/quests/"+url.PathEscape(st.QuestID), "Deleted.")
}

// formIntPtr is a whole-number form field as a PATCH field: nil when the
// form did not send it.
func formIntPtr(r *http.Request, name string) *int {
	if _, ok := r.PostForm[name]; !ok {
		return nil
	}
	n := formInt(r, name)
	return &n
}

// formBoolPtr is a checkbox as a PATCH field. An unchecked box sends
// nothing, so a form with the box also sends has_<name>; without it, the
// field was not sent.
func formBoolPtr(r *http.Request, name string) *bool {
	if r.PostFormValue("has_"+name) == "" {
		return nil
	}
	v := r.PostFormValue(name) != ""
	return &v
}

// ---- playtests --------------------------------------------------------------

// playtest is the game, playing the content as it stands. It compiles the
// content on every request, so an edit shows on the next click; build
// errors are shown above the game rather than stopping it.
func (s *Server) playtest() *play.Handler {
	return &play.Handler{
		Title: "Playtest",
		Base:  "/play",
		Back:  "/",
		Store: s.Store,
		Owner: auth.KeyIDPtr,
		Load: func(ctx context.Context) (*game.Game, string, error) {
			c, err := content.Load(ctx, s.Store)
			if err != nil {
				return nil, "", err
			}
			bundle, report := content.Compile(c)
			notice := ""
			if n := len(report.Errors); n > 0 {
				notice = fmt.Sprintf("The content has build errors (%d), so some of it may not play as written. See the Build page.", n)
			}
			return game.New(bundle), notice, nil
		},
		Setup: func(ctx context.Context, text string) ([]script.Effect, error) {
			names, err := app.LoadNames(ctx, s.Store)
			if err != nil {
				return nil, err
			}
			effects, err := script.ParseEffects(text, names)
			if err != nil {
				return nil, errors.New("setup: " + err.Error())
			}
			return effects, nil
		},
	}
}
