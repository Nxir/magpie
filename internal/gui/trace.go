package gui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/usage"
)

// traceJSON is the gateway's routing trace since the page last asked.
type traceJSON struct {
	gateway.TraceState
	Mine   bool        `json:"mine"` // this magpie serves the gateway: another's trace isn't here
	Now    time.Time   `json:"now"`
	Routes []routeJSON `json:"routes"`
}

type routeJSON struct {
	gateway.Route
	SessionTitle string  `json:"sessionTitle,omitempty"`
	Cost         float64 `json:"cost"`
	Priced       bool    `json:"priced"`
	Unpriced     int     `json:"unpriced"`
}

func pricedRoutes(routes []gateway.Route) []routeJSON {
	out := make([]routeJSON, 0, len(routes))
	priceOf := usage.NewPricer()
	ids := make([]string, 0, len(routes))
	for _, r := range routes {
		if r.Agent == "codex" {
			id := r.Session
			if r.ParentSession != "" {
				id = r.ParentSession
			}
			ids = append(ids, id)
		}
	}
	titles := sessions.CodexTitles(ids)
	for _, r := range routes {
		recs := make([]usage.Record, len(r.Usage))
		for i, u := range r.Usage {
			recs[i] = u.PricingRecord()
		}
		sum := priceOf(recs)
		// Old history and calls without token counts stay unknown, not free.
		priced := sum.Calls > sum.Unpriced && sum.Input+sum.Output > 0
		name := ""
		if r.Agent == "codex" {
			id := r.Session
			if r.ParentSession != "" {
				id = r.ParentSession
			}
			name = titles[id]
		}
		out = append(out, routeJSON{Route: r, SessionTitle: name, Cost: sum.Cost, Priced: priced, Unpriced: sum.Unpriced})
	}
	return out
}

// mainView is the tab the window is asked to open on, as the page's view
// parameter: the Routing page on one request (req, its id) when the tray
// panel's Routing tab asks for it. Anything but an id is dropped.
func mainView(q url.Values) string {
	view := q.Get("view")
	if req := q.Get("req"); view == "routing" && req != "" {
		if id, err := strconv.ParseInt(req, 10, 64); err == nil && id > 0 {
			view += "&req=" + strconv.FormatInt(id, 10)
		}
	}
	return view
}

// mainURL is the window's page for a view as mainView gives it, the
// request's id kept as its own parameter (the panel's link to one request,
// which escaping the whole of it lost).
func mainURL(view, query string) string { return "/?view=" + view + query }

// argView is a tab named on the command line (`magpie gui settings`, a
// restart to update) as ShowMain takes it: a name, never parameters.
func argView(s string) string { return url.QueryEscape(s) }

// traceRoutes serves the routing trace for the Gateway view to play: it
// waits up to 25 s for something to change after the seq it is given, so
// the page hears of a request as it happens.
func traceRoutes(mux *http.ServeMux) {
	// Names can arrive or change after a route finishes, independently of the
	// trace sequence. Read only the IDs the page currently lists.
	mux.HandleFunc("POST /api/gateway/session-titles", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"ids"`
		}
		r.Body = http.MaxBytesReader(rw, r.Body, 256<<10)
		if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.IDs) > 2000 {
			http.Error(rw, "invalid session IDs", http.StatusBadRequest)
			return
		}
		writeJSON(rw, sessions.CodexTitles(in.IDs))
	})
	mux.HandleFunc("GET /api/gateway/trace", func(rw http.ResponseWriter, r *http.Request) {
		gw := served.Load()
		out := traceJSON{TraceState: gateway.TraceState{Routes: []gateway.Route{}}, Mine: gw != nil}
		if gw != nil {
			after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
			var wait time.Duration
			if r.URL.Query().Get("wait") != "" {
				wait = 25 * time.Second
			}
			out.TraceState = gw.Trace(r.Context(), after, wait)
		}
		out.Now = time.Now()
		out.Routes = pricedRoutes(out.TraceState.Routes)
		writeJSON(rw, out)
	})
	// the routes of a day gone by, from the history the gateway keeps on
	// disk — read whichever magpie serves the gateway
	mux.HandleFunc("GET /api/gateway/history", func(rw http.ResponseWriter, r *http.Request) {
		days, routes, cut := gateway.History(r.URL.Query().Get("day"))
		writeJSON(rw, map[string]any{"days": days, "routes": pricedRoutes(routes), "cut": cut})
	})
	// an account's rest lifted by hand: verified with its vendor, say
	mux.HandleFunc("POST /api/gateway/unrest", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Key string `json:"key"`
		}
		gw := served.Load()
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Key == "" || gw == nil {
			http.Error(rw, "nothing to lift", http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]bool{"lifted": gw.Unrest(in.Key)})
	})
}
