// Package api is the desk's HTTP surface: a signed webhook for check results, acknowledgements and posts from
// responders, the incident and page lists, and the public status page (JSON and HTML).
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/desk"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/store"
)

// Server wires the desk to HTTP.
type Server struct {
	Desk   *desk.Desk
	Store  store.Store
	Index  *oncall.Index
	Secret []byte // signs result webhooks; empty refuses every webhook
	Now    func() time.Time
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/results", s.results)
	mux.HandleFunc("POST /v1/incidents/{id}/ack", s.ack)
	mux.HandleFunc("POST /v1/incidents/{id}/posts", s.post)
	mux.HandleFunc("GET /v1/incidents", s.incidents)
	mux.HandleFunc("GET /v1/incidents/{id}", s.incident)
	mux.HandleFunc("GET /v1/pages", s.pages)
	mux.HandleFunc("GET /v1/status", s.statusJSON)
	mux.HandleFunc("GET /status", s.statusHTML)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return mux
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

type problem struct {
	Error      string `json:"error"`
	Violations any    `json:"violations,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) { writeJSON(w, code, problem{Error: msg}) }

// Sign returns the signature header value for a webhook body.
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (s *Server) results(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		fail(w, http.StatusBadRequest, "the body could not be read")
		return
	}
	sig := r.Header.Get("X-Desk-Signature")
	if len(s.Secret) == 0 || !hmac.Equal([]byte(sig), []byte(Sign(s.Secret, body))) {
		fail(w, http.StatusUnauthorized, "the X-Desk-Signature header does not match the body: sign it with the webhook secret")
		return
	}
	var batch struct {
		Results []oncall.Result `json:"results"`
	}
	if err := json.Unmarshal(body, &batch); err != nil || len(batch.Results) == 0 {
		var one oncall.Result
		if err2 := json.Unmarshal(body, &one); err2 != nil || one.ID == "" {
			fail(w, http.StatusBadRequest, `send {"results":[...]} or one result with an id`)
			return
		}
		batch.Results = []oncall.Result{one}
	}
	accepted, dupes := 0, 0
	for _, res := range batch.Results {
		fresh, err := s.Desk.Ingest(r.Context(), res)
		if errors.Is(err, desk.ErrUnknown) {
			fail(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "the result could not be stored; retry the delivery")
			return
		}
		if fresh {
			accepted++
		} else {
			dupes++
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"accepted": accepted, "duplicates": dupes})
}

func (s *Server) ack(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Responder string `json:"responder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Responder == "" {
		fail(w, http.StatusBadRequest, `send {"responder":"<who>"}`)
		return
	}
	err := s.Desk.Ack(r.Context(), r.PathValue("id"), in.Responder, s.now())
	if errors.Is(err, desk.ErrUnknown) {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	inc, _, _ := s.Store.Incident(r.Context(), r.PathValue("id"))
	writeJSON(w, http.StatusOK, inc)
}

func (s *Server) post(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Author string `json:"author"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Author == "" || strings.TrimSpace(in.Text) == "" {
		fail(w, http.StatusBadRequest, `send {"author":"<who>","text":"<what customers should read>"}`)
		return
	}
	p, err := s.Desk.PostManual(r.Context(), r.PathValue("id"), in.Author, in.Text, s.now())
	var rej desk.ErrRejected
	switch {
	case errors.As(err, &rej):
		writeJSON(w, http.StatusUnprocessableEntity, problem{Error: "not published: change the text and send it again", Violations: rej.Violations})
	case errors.Is(err, desk.ErrUnknown):
		fail(w, http.StatusNotFound, err.Error())
	case err != nil:
		fail(w, http.StatusConflict, err.Error())
	default:
		writeJSON(w, http.StatusCreated, p)
	}
}

func (s *Server) incidents(w http.ResponseWriter, r *http.Request) {
	xs, err := s.Store.Incidents(r.Context(), r.URL.Query().Get("open") == "true")
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if xs == nil {
		xs = []oncall.Incident{}
	}
	writeJSON(w, http.StatusOK, xs)
}

func (s *Server) incident(w http.ResponseWriter, r *http.Request) {
	inc, ok, err := s.Store.Incident(r.Context(), r.PathValue("id"))
	switch {
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	case !ok:
		fail(w, http.StatusNotFound, "no incident "+r.PathValue("id"))
	default:
		writeJSON(w, http.StatusOK, inc)
	}
}

func (s *Server) pages(w http.ResponseWriter, r *http.Request) {
	xs, err := s.Store.Pages(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if xs == nil {
		xs = []oncall.Page{}
	}
	writeJSON(w, http.StatusOK, xs)
}

// ComponentStatus is one row of the public status page.
type ComponentStatus struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // operational, degraded, outage
}

// Status is the public page: components and the posts of the last seven days, newest first.
type Status struct {
	Components []ComponentStatus `json:"components"`
	Posts      []oncall.Post     `json:"posts"`
}

// PublicStatus derives the page from what was posted: a component is down only while a post named it and its
// incident is still open, so the page never shows what was not announced.
func (s *Server) PublicStatus(r *http.Request) (Status, error) {
	posts, err := s.Store.Posts(r.Context())
	if err != nil {
		return Status{}, err
	}
	open, err := s.Store.Incidents(r.Context(), true)
	if err != nil {
		return Status{}, err
	}
	sev := map[string]string{}
	for _, inc := range open {
		sev[inc.ID] = inc.Severity
	}
	state := map[string]string{}
	since := s.now().Add(-7 * 24 * time.Hour)
	out := Status{Posts: []oncall.Post{}}
	for _, p := range posts {
		if !p.At.Before(since) {
			out.Posts = append(out.Posts, p)
		}
		sv, isOpen := sev[p.IncidentID]
		if !isOpen || p.Kind != "investigating" {
			continue
		}
		for _, c := range p.Components {
			if sv == "major" || state[c] == "" {
				state[c] = map[string]string{"major": "outage", "minor": "degraded"}[sv]
			}
		}
	}
	sort.Slice(out.Posts, func(i, j int) bool { return out.Posts[i].At.After(out.Posts[j].At) })
	for _, c := range s.Index.Components {
		if !c.Public {
			continue
		}
		st := state[c.ID]
		if st == "" {
			st = "operational"
		}
		out.Components = append(out.Components, ComponentStatus{ID: c.ID, Name: c.Name, Status: st})
	}
	return out, nil
}

func (s *Server) statusJSON(w http.ResponseWriter, r *http.Request) {
	st, err := s.PublicStatus(r)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

var page = template.Must(template.New("status").Funcs(template.FuncMap{
	"when": func(t time.Time) string { return t.UTC().Format("2 Jan 15:04 UTC") },
	"label": func(s string) string {
		return map[string]string{"operational": "Working", "degraded": "Degraded in some regions", "outage": "Not working"}[s]
	},
}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Status</title><style>
body{font:16px/1.5 system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem;color:#1d2330}
li{display:flex;justify-content:space-between;padding:.5rem 0;border-bottom:1px solid #e3e6ec}
.operational{color:#2f6f4f}.degraded{color:#9a5b00}.outage{color:#b3261e}
article{margin:1rem 0}time{color:#5b6472;font-size:.875rem}
</style></head><body><h1>Service status</h1><ul style="list-style:none;padding:0">
{{range .Components}}<li><span>{{.Name}}</span><span class="{{.Status}}">{{label .Status}}</span></li>{{end}}
</ul><h2>Updates</h2>{{range .Posts}}<article><time>{{when .At}}</time><p>{{.Text}}</p></article>{{else}}<p>No incidents in the last seven days.</p>{{end}}
</body></html>`))

func (s *Server) statusHTML(w http.ResponseWriter, r *http.Request) {
	st, err := s.PublicStatus(r)
	if err != nil {
		http.Error(w, "the status page could not be built; try again", http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	page.Execute(w, st)
}
