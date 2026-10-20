package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"central-automacoes/internal/domain"
)

func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		problem(w, http.StatusBadRequest, "query string inválida")
		return
	}
	for key, values := range query {
		if key != "timezone" || len(values) != 1 {
			problem(w, 400, "parâmetro do dashboard inválido ou repetido")
			return
		}
	}
	timezone := "UTC"
	if values, ok := query["timezone"]; ok {
		timezone = values[0]
		if timezone == "" || timezone == "Local" || strings.ContainsAny(timezone, "\x00\r\n") {
			problem(w, 400, "timezone IANA inválido")
			return
		}
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		problem(w, 400, "timezone IANA inválido")
		return
	}
	now := time.Now().UTC()
	snapshot, err := a.store.PanelSnapshot(r.Context(), domain.PanelStart(now, location), now)
	if err != nil {
		a.failure(w, err)
		return
	}
	respond(w, http.StatusOK, domain.BuildDashboard(snapshot, now, location))
}
