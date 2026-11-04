package web

import (
	"bytes"
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
)

// The server renders only what needs no app: the sign-in and join pages
// here, and the playtest (package play). The authoring tool is the React app in
// package ui.

func parsePages() map[string]*template.Template {
	pages := map[string]*template.Template{}
	for _, name := range []string{"login", "join"} {
		pages[name] = template.Must(template.New("layout.html").
			ParseFS(templateFS, "templates/layout.html", "templates/"+name+".html"))
	}
	return pages
}

// render writes a page through a buffer, so a template error becomes a 500
// rather than half a page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data map[string]any) {
	data["AppName"] = app.Name
	data["GoogleOn"] = s.Google.enabled()
	data["SignedIn"] = auth.KeyID(r.Context()) != ""
	data["Path"] = r.URL.RequestURI()
	var buf bytes.Buffer
	if err := s.pages[name].Execute(&buf, data); err != nil {
		slog.Error("render", "page", name, "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// fail answers a request whose lookup failed.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, app.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	slog.Error("page", "path", r.URL.Path, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// ---- redirects to where a node, choice or stage lives -----------------------

func (s *Server) nodeRedirect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := app.Access(r.Context(), s.Store, "node", id); err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := s.Store.GetNode(r.Context(), id)
	if err != nil {
		s.fail(w, r, app.NotFound(err, "node", id))
		return
	}
	http.Redirect(w, r, s.Links.Node(n.ConversationID, n.ID), http.StatusSeeOther)
}

func (s *Server) choiceRedirect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := app.Access(r.Context(), s.Store, "choice", id); err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.Store.GetChoice(r.Context(), id)
	if err != nil {
		s.fail(w, r, app.NotFound(err, "choice", id))
		return
	}
	n, err := s.Store.GetNode(r.Context(), c.FromNodeID)
	if err != nil {
		s.fail(w, r, app.NotFound(err, "node", c.FromNodeID))
		return
	}
	http.Redirect(w, r, s.Links.Conversation(n.ConversationID)+"#choice-"+c.ID, http.StatusSeeOther)
}

func (s *Server) stageRedirect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := app.Access(r.Context(), s.Store, "quest_stage", id); err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.Store.GetQuestStage(r.Context(), id)
	if err != nil {
		s.fail(w, r, app.NotFound(err, "stage", id))
		return
	}
	http.Redirect(w, r, s.Links.Quest(st.QuestID)+"#stage-"+st.ID, http.StatusSeeOther)
}
