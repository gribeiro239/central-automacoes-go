package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"central-automacoes/internal/domain"
)

type API struct {
	store   domain.Store
	storage string
	logger  *slog.Logger
}

func New(store domain.Store, storage string, logger *slog.Logger) http.Handler {
	a := &API{store: store, storage: storage, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.panel)
	mux.HandleFunc("GET /assets/", a.asset)
	mux.HandleFunc("GET /dashboard", a.dashboard)
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("POST /automations", a.create)
	mux.HandleFunc("GET /automations", a.list)
	mux.HandleFunc("GET /automations/{id}", a.get)
	mux.HandleFunc("PATCH /automations/{id}", a.patch)
	mux.HandleFunc("POST /automations/{id}/runs", a.enqueue)
	mux.HandleFunc("POST /automations/{id}/archive", a.archive)
	mux.HandleFunc("POST /automations/{id}/schedule/pause", a.pause)
	mux.HandleFunc("POST /automations/{id}/schedule/resume", a.resume)
	mux.HandleFunc("GET /runs", a.runs)
	mux.HandleFunc("GET /runs/{id}", a.run)
	mux.HandleFunc("GET /runs/{id}/attempts", a.attempts)
	mux.HandleFunc("POST /runs/{id}/retry", a.retry)
	mux.HandleFunc("POST /runs/{id}/cancel", a.cancel)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if recover() != nil {
				a.logger.Error("panic na API", "method", r.Method, "path", r.URL.Path)
				problem(w, 500, "falha interna")
			}
		}()
		mux.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func problem(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if h := r.Header.Get("Content-Type"); h != "" {
		media, _, err := mime.ParseMediaType(h)
		if err != nil || media != "application/json" {
			problem(w, 415, "use Content-Type: application/json")
			return false
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		problem(w, 400, "JSON inválido, campo desconhecido ou corpo acima de 128 KiB")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		problem(w, 400, "envie somente um objeto JSON")
		return false
	}
	return true
}

func (a *API) failure(w http.ResponseWriter, err error) {
	var validation domain.ValidationError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		problem(w, 404, err.Error())
	case errors.Is(err, domain.ErrDisabled):
		problem(w, 409, err.Error())
	case errors.Is(err, domain.ErrArchived), errors.Is(err, domain.ErrConflict):
		problem(w, 409, err.Error())
	case errors.As(err, &validation):
		problem(w, 400, err.Error())
	default:
		a.logger.Error("erro de persistência", "error", err)
		problem(w, 500, "falha ao acessar armazenamento")
	}
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		problem(w, 503, "armazenamento indisponível")
		return
	}
	respond(w, 200, map[string]string{"status": "ok", "storage": a.storage})
}
func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var input *domain.CreateAutomation
	if !decode(w, r, &input) {
		return
	}
	if input == nil {
		problem(w, 400, "envie um objeto JSON")
		return
	}
	automation, err := input.Automation()
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	automation, err = a.store.CreateAutomation(r.Context(), automation)
	if err != nil {
		a.failure(w, err)
		return
	}
	w.Header().Set("Location", "/automations/"+automation.ID)
	respond(w, 201, automation)
}
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	includeArchived, err := archiveFilter(r)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	v, err := a.store.ListAutomations(r.Context())
	if err != nil {
		a.failure(w, err)
		return
	}
	visible := make([]domain.Automation, 0, len(v))
	for _, item := range v {
		if includeArchived || item.ArchivedAt == nil {
			visible = append(visible, item)
		}
	}
	respond(w, 200, visible)
}
func (a *API) get(w http.ResponseWriter, r *http.Request) {
	v, err := a.store.GetAutomation(r.Context(), r.PathValue("id"))
	if err != nil {
		a.failure(w, err)
		return
	}
	respond(w, 200, v)
}
func (a *API) patch(w http.ResponseWriter, r *http.Request) {
	var input *domain.PatchAutomation
	if !decode(w, r, &input) {
		return
	}
	if input == nil {
		problem(w, 400, "envie um objeto JSON")
		return
	}
	v, err := a.store.PatchAutomation(r.Context(), r.PathValue("id"), *input)
	if err != nil {
		a.failure(w, err)
		return
	}
	respond(w, 200, v)
}
func (a *API) enqueue(w http.ResponseWriter, r *http.Request) {
	v, err := a.store.Enqueue(r.Context(), r.PathValue("id"))
	if err != nil {
		a.failure(w, err)
		return
	}
	w.Header().Set("Location", "/runs/"+v.ID)
	respond(w, 202, v)
}
func (a *API) runs(w http.ResponseWriter, r *http.Request) {
	filter, err := parseRunFilter(r)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	v, err := a.store.QueryRuns(r.Context(), filter)
	if err != nil {
		a.failure(w, err)
		return
	}
	respond(w, 200, v)
}
func (a *API) run(w http.ResponseWriter, r *http.Request) {
	v, err := a.store.GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		a.failure(w, err)
		return
	}
	respond(w, 200, v)
}
func (a *API) attempts(w http.ResponseWriter, r *http.Request) {
	v, err := a.store.ListAttempts(r.Context(), r.PathValue("id"))
	if err != nil {
		a.failure(w, err)
		return
	}
	respond(w, 200, v)
}
