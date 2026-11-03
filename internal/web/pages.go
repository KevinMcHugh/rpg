package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	"github.com/KevinMcHugh/rpg/internal/server"
	"github.com/KevinMcHugh/rpg/internal/server/build"
	"github.com/KevinMcHugh/rpg/internal/server/characters"
	"github.com/KevinMcHugh/rpg/internal/server/conversations"
	"github.com/KevinMcHugh/rpg/internal/server/notes"
)

// The pages drive the same endpoints as the JSON API and MCP, through
// server.BuildViewModel, and render the view models as HTML.

// noteBox is what the "notes" template renders: the thread on one target,
// and the form to add to it.
type noteBox struct {
	Kind, ID string
	Notes    []notes.Note
	Human    bool   // may resolve and dismiss
	Back     string // where a form sends the browser afterwards
}

var funcs = template.FuncMap{
	"notebox": func(kind, id string, ns []notes.Note, human bool, back string) noteBox {
		return noteBox{Kind: kind, ID: id, Notes: ns, Human: human, Back: back}
	},
	"noteKinds": func() []string { return notes.Kinds },
	"label":     func(s string) string { return strings.ReplaceAll(s, "_", " ") },
}

// excerpt shortens s to n characters, on one line, for a menu or a link.
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func parsePages() map[string]*template.Template {
	pages := map[string]*template.Template{}
	for _, name := range []string{"login", "index", "character", "conversation", "notes", "build"} {
		pages[name] = template.Must(template.New("layout.html").Funcs(funcs).
			ParseFS(templateFS, "templates/layout.html", "templates/"+name+".html"))
	}
	return pages
}

// render writes a page through a buffer, so a template error becomes a 500
// rather than half a page. Every page learns whether the visitor is signed
// in, whether they may change content (Human), and the one-line message a
// redirect left in ?msg= or ?err=.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data map[string]any) {
	data["AppName"] = app.Name
	data["SignedIn"] = auth.KeyID(r.Context()) != ""
	data["Human"] = auth.IsHuman(r.Context())
	data["Path"] = r.URL.Path
	if _, ok := data["Flash"]; !ok {
		data["Flash"] = r.URL.Query().Get("msg")
	}
	if _, ok := data["Error"]; !ok {
		data["Error"] = r.URL.Query().Get("err")
	}
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

// statusOf maps the endpoints' errors to HTTP statuses.
func statusOf(err error) int {
	switch {
	case errors.Is(err, app.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, app.ErrBadInput):
		return http.StatusUnprocessableEntity
	case errors.Is(err, app.ErrForbidden):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

// fail answers a page request whose endpoint failed.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := statusOf(err)
	if status == http.StatusInternalServerError {
		slog.Error("page", "path", r.URL.Path, "err", err)
		http.Error(w, "internal error", status)
		return
	}
	http.Error(w, err.Error(), status)
}

// redirect sends the browser to path with a one-line message, err or not,
// that the next page shows (post/redirect/get, with no script and no
// session state). A #fragment on path is kept after the message.
func redirect(w http.ResponseWriter, r *http.Request, path, msg string, isErr bool) {
	key := "msg"
	if isErr {
		key = "err"
	}
	path, frag, _ := strings.Cut(path, "#")
	if frag != "" {
		frag = "#" + frag
	}
	http.Redirect(w, r, path+"?"+key+"="+url.QueryEscape(msg)+frag, http.StatusSeeOther)
}

// after finishes a form post: back to path with "Saved." or the reason it
// was refused. Only an unexpected error is a bare error page.
func (s *Server) after(w http.ResponseWriter, r *http.Request, err error, path, ok string) {
	switch {
	case err == nil:
		redirect(w, r, path, ok, false)
	case errors.Is(err, app.ErrBadInput), errors.Is(err, app.ErrForbidden):
		redirect(w, r, path, err.Error(), true)
	default:
		s.fail(w, r, err)
	}
}

// formPtr is a form field as a PATCH field: nil when the form did not send
// it.
func formPtr(r *http.Request, name string) *string {
	if _, ok := r.PostForm[name]; !ok {
		return nil
	}
	v := r.PostFormValue(name)
	return &v
}

// formInt is a whole-number form field; anything else reads as 0.
func formInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue(name)))
	return n
}

// notesByTarget groups notes by "kind:id", the key the templates look them
// up by.
func notesByTarget(ns []notes.Note) map[string][]notes.Note {
	out := map[string][]notes.Note{}
	for _, n := range ns {
		k := n.TargetKind + ":" + n.TargetID
		out[k] = append(out[k], n)
	}
	return out
}

// allNotes is every note, for the pages that show them beside the content.
func (s *Server) allNotes(r *http.Request) (map[string][]notes.Note, error) {
	ns, err := server.BuildViewModel(r.Context(), notes.ListEndpoint{Store: s.Store, Links: s.Links}, apigen.ListNotesRequestObject{})
	return notesByTarget(ns), err
}

// ---- index ------------------------------------------------------------------

func (s *Server) indexPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	convs, err := server.BuildViewModel(ctx, conversations.ListEndpoint{Store: s.Store, Links: s.Links}, apigen.ListConversationsRequestObject{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	chars, err := server.BuildViewModel(ctx, characters.ListEndpoint{Store: s.Store, Links: s.Links}, apigen.ListCharactersRequestObject{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	report, err := server.BuildViewModel(ctx, build.Endpoint{Store: s.Store, Links: s.Links}, apigen.GetBuildReportRequestObject{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byTarget, err := s.allNotes(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "index", map[string]any{
		"Conversations": convs, "Characters": chars, "Report": report, "Notes": byTarget,
	})
}

// ---- characters -------------------------------------------------------------

func (s *Server) characterPage(w http.ResponseWriter, r *http.Request) {
	c, err := server.BuildViewModel(r.Context(), characters.GetEndpoint{Store: s.Store, Links: s.Links}, apigen.GetCharacterRequestObject{CharacterId: chi.URLParam(r, "id")})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byTarget, err := s.allNotes(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "character", map[string]any{"Character": c, "Notes": byTarget["character:"+c.ID]})
}

func (s *Server) createCharacter(w http.ResponseWriter, r *http.Request) {
	c, err := server.BuildViewModel(r.Context(), characters.CreateEndpoint{Store: s.Store, Links: s.Links}, apigen.CreateCharacterRequestObject{
		Body: &apigen.CharacterCreate{Name: r.FormValue("name"), Bible: r.FormValue("bible")},
	})
	if err != nil {
		s.after(w, r, err, "/", "")
		return
	}
	redirect(w, r, "/characters/"+url.PathEscape(c.ID), "Saved.", false)
}

func (s *Server) updateCharacter(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = r.ParseForm()
	_, err := server.BuildViewModel(r.Context(), characters.UpdateEndpoint{Store: s.Store, Links: s.Links}, apigen.UpdateCharacterRequestObject{
		CharacterId: id,
		Body:        &apigen.CharacterUpdate{Name: formPtr(r, "name"), Bible: formPtr(r, "bible")},
	})
	s.after(w, r, err, "/characters/"+url.PathEscape(id), "Saved.")
}

func (s *Server) deleteCharacter(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, err := server.BuildViewModel(r.Context(), characters.DeleteEndpoint{Store: s.Store}, apigen.DeleteCharacterRequestObject{CharacterId: id})
	if err != nil {
		s.after(w, r, err, "/characters/"+url.PathEscape(id), "")
		return
	}
	redirect(w, r, "/", "Deleted.", false)
}

// ---- conversations ----------------------------------------------------------

func (s *Server) conversationPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	g, err := server.BuildViewModel(ctx, conversations.GetEndpoint{Store: s.Store, Links: s.Links}, apigen.GetConversationRequestObject{ConversationId: chi.URLParam(r, "id")})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	chars, err := server.BuildViewModel(ctx, characters.ListEndpoint{Store: s.Store, Links: s.Links}, apigen.ListCharactersRequestObject{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byTarget, err := s.allNotes(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Number the lines and label them for the menus that pick one.
	numbers, labels := map[string]int{}, map[string]string{}
	for i, n := range g.Nodes {
		speaker := n.SpeakerName
		if speaker == "" {
			speaker = "Narrator"
		}
		numbers[n.ID] = i + 1
		labels[n.ID] = fmt.Sprintf("%d. %s: %s", i+1, speaker, excerpt(n.Text, 50))
	}
	s.render(w, r, http.StatusOK, "conversation", map[string]any{
		"Graph": g, "Characters": chars, "Notes": byTarget, "Numbers": numbers, "Labels": labels,
	})
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	g, err := server.BuildViewModel(r.Context(), conversations.CreateEndpoint{Store: s.Store, Links: s.Links}, apigen.CreateConversationRequestObject{
		Body: &apigen.ConversationCreate{Title: r.FormValue("title"), Summary: r.FormValue("summary")},
	})
	if err != nil {
		s.after(w, r, err, "/", "")
		return
	}
	redirect(w, r, "/conversations/"+url.PathEscape(g.Conversation.ID), "Saved.", false)
}

func (s *Server) updateConversation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = r.ParseForm()
	_, err := server.BuildViewModel(r.Context(), conversations.UpdateEndpoint{Store: s.Store, Links: s.Links}, apigen.UpdateConversationRequestObject{
		ConversationId: id,
		Body: &apigen.ConversationUpdate{
			Title:       formPtr(r, "title"),
			Summary:     formPtr(r, "summary"),
			StartNodeId: formPtr(r, "start_node_id"),
		},
	})
	s.after(w, r, err, "/conversations/"+url.PathEscape(id), "Saved.")
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, err := server.BuildViewModel(r.Context(), conversations.DeleteEndpoint{Store: s.Store}, apigen.DeleteConversationRequestObject{ConversationId: id})
	if err != nil {
		s.after(w, r, err, "/conversations/"+url.PathEscape(id), "")
		return
	}
	redirect(w, r, "/", "Deleted.", false)
}

// ---- nodes and choices ------------------------------------------------------

// conversationOf is the page a node lives on, for redirects.
func (s *Server) conversationOf(r *http.Request, nodeID string) (string, error) {
	n, err := s.Store.GetNode(r.Context(), nodeID)
	if err != nil {
		return "", app.NotFound(err, "node", nodeID)
	}
	return s.Links.Node(n.ConversationID, n.ID), nil
}

func (s *Server) nodeRedirect(w http.ResponseWriter, r *http.Request) {
	to, err := s.conversationOf(r, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) choiceRedirect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
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

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	conv := "/conversations/" + url.PathEscape(chi.URLParam(r, "id"))
	n, err := server.BuildViewModel(r.Context(), conversations.CreateNodeEndpoint{Store: s.Store, Links: s.Links}, apigen.CreateNodeRequestObject{
		ConversationId: chi.URLParam(r, "id"),
		Body:           &apigen.NodeCreate{SpeakerId: r.FormValue("speaker_id"), Text: r.FormValue("text")},
	})
	if err != nil {
		s.after(w, r, err, conv+"#new-node", "")
		return
	}
	redirect(w, r, conv+"#node-"+n.ID, "Saved.", false)
}

func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = r.ParseForm()
	back, err := s.conversationOf(r, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, err = server.BuildViewModel(r.Context(), conversations.UpdateNodeEndpoint{Store: s.Store, Links: s.Links}, apigen.UpdateNodeRequestObject{
		NodeId: id,
		Body:   &apigen.NodeUpdate{SpeakerId: formPtr(r, "speaker_id"), Text: formPtr(r, "text")},
	})
	s.after(w, r, err, back, "Saved.")
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	back, err := s.conversationOf(r, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, err = server.BuildViewModel(r.Context(), conversations.DeleteNodeEndpoint{Store: s.Store}, apigen.DeleteNodeRequestObject{NodeId: id})
	s.after(w, r, err, back, "Deleted.")
}

func (s *Server) createChoice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	back, err := s.conversationOf(r, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, err = server.BuildViewModel(r.Context(), conversations.CreateChoiceEndpoint{Store: s.Store}, apigen.CreateChoiceRequestObject{
		NodeId: id,
		Body: &apigen.ChoiceCreate{
			Text:      r.FormValue("text"),
			ToNodeId:  r.FormValue("to_node_id"),
			SortOrder: formInt(r, "sort_order"),
		},
	})
	s.after(w, r, err, back, "Saved.")
}

func (s *Server) updateChoice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = r.ParseForm()
	c, err := s.Store.GetChoice(r.Context(), id)
	if err != nil {
		s.fail(w, r, app.NotFound(err, "choice", id))
		return
	}
	back, err := s.conversationOf(r, c.FromNodeID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var order *int
	if _, ok := r.PostForm["sort_order"]; ok {
		n := formInt(r, "sort_order")
		order = &n
	}
	_, err = server.BuildViewModel(r.Context(), conversations.UpdateChoiceEndpoint{Store: s.Store}, apigen.UpdateChoiceRequestObject{
		ChoiceId: id,
		Body:     &apigen.ChoiceUpdate{Text: formPtr(r, "text"), ToNodeId: formPtr(r, "to_node_id"), SortOrder: order},
	})
	s.after(w, r, err, back, "Saved.")
}

func (s *Server) deleteChoice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, err := s.Store.GetChoice(r.Context(), id)
	if err != nil {
		s.fail(w, r, app.NotFound(err, "choice", id))
		return
	}
	back, err := s.conversationOf(r, c.FromNodeID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, err = server.BuildViewModel(r.Context(), conversations.DeleteChoiceEndpoint{Store: s.Store}, apigen.DeleteChoiceRequestObject{ChoiceId: id})
	s.after(w, r, err, back, "Deleted.")
}

// ---- notes ------------------------------------------------------------------

func (s *Server) notesPage(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	if status == "all" {
		status = ""
	}
	ns, err := server.BuildViewModel(r.Context(), notes.ListEndpoint{Store: s.Store, Links: s.Links}, apigen.ListNotesRequestObject{
		Params: apigen.ListNotesParams{Status: apigen.NoteStatus(status)},
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if status == "" {
		status = "all"
	}
	s.render(w, r, http.StatusOK, "notes", map[string]any{"Notes": ns, "Status": status, "Statuses": append([]string{"all"}, notes.Statuses...)})
}

func (s *Server) createNote(w http.ResponseWriter, r *http.Request) {
	back := safeNext(r.FormValue("back"))
	_, err := server.BuildViewModel(r.Context(), notes.CreateEndpoint{Store: s.Store, Links: s.Links}, apigen.CreateNoteRequestObject{
		Body: &apigen.NoteCreate{
			TargetKind: apigen.NoteTargetKind(r.FormValue("target_kind")),
			TargetId:   r.FormValue("target_id"),
			Kind:       apigen.NoteKind(r.FormValue("kind")),
			Body:       r.FormValue("body"),
			ParentId:   r.FormValue("parent_id"),
		},
	})
	s.after(w, r, err, back, "Noted.")
}

func (s *Server) setNoteStatus(w http.ResponseWriter, r *http.Request) {
	back := safeNext(r.FormValue("back"))
	_, err := server.BuildViewModel(r.Context(), notes.UpdateEndpoint{Store: s.Store, Links: s.Links}, apigen.UpdateNoteRequestObject{
		NoteId: chi.URLParam(r, "id"),
		Body:   &apigen.NoteUpdate{Status: apigen.NoteStatus(r.FormValue("status"))},
	})
	s.after(w, r, err, back, "Saved.")
}

func (s *Server) deleteNote(w http.ResponseWriter, r *http.Request) {
	back := safeNext(r.FormValue("back"))
	_, err := server.BuildViewModel(r.Context(), notes.DeleteEndpoint{Store: s.Store}, apigen.DeleteNoteRequestObject{NoteId: chi.URLParam(r, "id")})
	s.after(w, r, err, back, "Deleted.")
}

// ---- build ------------------------------------------------------------------

func (s *Server) buildPage(w http.ResponseWriter, r *http.Request) {
	report, err := server.BuildViewModel(r.Context(), build.Endpoint{Store: s.Store, Links: s.Links}, apigen.GetBuildReportRequestObject{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "build", map[string]any{"Report": report})
}
