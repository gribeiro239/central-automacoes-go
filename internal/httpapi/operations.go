package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"central-automacoes/internal/domain"
)

func archiveFilter(r *http.Request) (bool, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false, errors.New("query string inválida")
	}
	for k, v := range q {
		if k != "include_archived" || len(v) != 1 {
			return false, errors.New("parâmetro de lista inválido")
		}
	}
	raw := q.Get("include_archived")
	if raw == "" {
		if q.Has("include_archived") {
			return false, errors.New("include_archived deve ser true ou false")
		}
		return false, nil
	}
	if raw != "true" && raw != "false" {
		return false, errors.New("include_archived deve ser true ou false")
	}
	return raw == "true", nil
}

func parseRunFilter(r *http.Request) (domain.RunFilter, error) {
	f := domain.RunFilter{Limit: 100}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return f, errors.New("query string inválida")
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return f, errors.New("filtro vazio ou repetido")
		}
		switch key {
		case "automation_id", "status", "from", "to", "limit", "offset":
		default:
			return f, errors.New("filtro desconhecido")
		}
	}
	f.AutomationID = q.Get("automation_id")
	f.Status = domain.Status(q.Get("status"))
	for _, field := range []struct {
		key    string
		target **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if raw := q.Get(field.key); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return f, errors.New("from e to devem usar RFC3339 com fuso horário")
			}
			value = value.UTC()
			*field.target = &value
		}
	}
	if raw := q.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 500 {
			return f, errors.New("limit deve estar entre 1 e 500")
		}
		f.Limit = value
	}
	if raw := q.Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return f, errors.New("offset deve ser inteiro >=0")
		}
		f.Offset = value
	}
	return f, f.Validate()
}

func (a *API) archive(w http.ResponseWriter, r *http.Request) {
	v, err := a.store.ArchiveAutomation(r.Context(), r.PathValue("id"))
	if err != nil {
		a.failure(w, err)
		return
	}
	respond(w, 200, v)
}
func (a *API) pause(w http.ResponseWriter, r *http.Request)  { a.setPaused(w, r, true) }
func (a *API) resume(w http.ResponseWriter, r *http.Request) { a.setPaused(w, r, false) }
func (a *API) setPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	v, err := a.store.SetSchedulePaused(r.Context(), r.PathValue("id"), paused)
	if err != nil {
		a.failure(w, err)
		return
	}
	respond(w, 200, v)
}
func (a *API) retry(w http.ResponseWriter, r *http.Request) {
	v, err := a.store.Rerun(r.Context(), r.PathValue("id"))
	if err != nil {
		a.failure(w, err)
		return
	}
	w.Header().Set("Location", "/runs/"+v.ID)
	respond(w, 202, v)
}
func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	v, err := a.store.CancelRun(r.Context(), r.PathValue("id"))
	if err != nil {
		a.failure(w, err)
		return
	}
	status := 200
	if v.Status == domain.Running {
		status = 202
	}
	respond(w, status, v)
}
