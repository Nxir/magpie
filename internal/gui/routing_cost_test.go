package gui

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/usage"
)

func TestRoutingEffectiveCosts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	save := func(input float64) {
		t.Helper()
		if err := settings.Save(settings.Settings{ModelPrices: map[string]settings.ModelPrice{
			"relay/m": {Input: &input, Output: new(float64(8)), CacheRead: new(float64(0.5)), CacheWrite: new(float64(2.5))},
			"free/m":  {Input: new(float64(0)), Output: new(float64(0)), CacheRead: new(float64(0)), CacheWrite: new(float64(0))},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	save(2)
	routes := []gateway.Route{
		{ID: 1, Session: "conversation", ParentSession: "parent-conversation", Usage: []usage.Record{
			{Provider: "relay", Model: "m", Input: 2000, Output: 500, CacheRead: 4000, CacheWrite: 1000},
			{Provider: "relay", Model: "m", Input: 1000, Output: 100},
		}},
		{ID: 2, Usage: []usage.Record{{Provider: "free", Model: "m", Input: 1000}}},
		{ID: 3, Tokens: 1000}, // old history: no token tiers, price unknown
	}
	got := pricedRoutes(routes)
	if !got[0].Priced || math.Abs(got[0].Cost-0.0153) > 1e-12 {
		t.Fatalf("all tiers and attempts: %+v", got[0])
	}
	if !got[1].Priced || got[1].Cost != 0 {
		t.Fatalf("explicit zero price: %+v", got[1])
	}
	if got[2].Priced {
		t.Fatalf("old history treated as free: %+v", got[2])
	}
	save(4)
	if updated := pricedRoutes(routes)[0]; math.Abs(updated.Cost-0.0213) > 1e-12 {
		t.Fatalf("price change ignored: %+v", updated)
	}
	// The enriched route list must override the embedded trace list in JSON.
	b, err := json.Marshal(traceJSON{TraceState: gateway.TraceState{Seq: 1, Routes: routes}, Routes: got})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Routes []routeJSON `json:"routes"`
		Seq    int64       `json:"seq"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Seq != 1 || len(decoded.Routes) != 3 || !decoded.Routes[0].Priced || decoded.Routes[0].Session != "conversation" || decoded.Routes[0].ParentSession != "parent-conversation" {
		t.Fatalf("API response %s", b)
	}
}
