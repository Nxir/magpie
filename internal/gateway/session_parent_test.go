package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTitleParentSession(t *testing.T) {
	for _, tc := range []struct {
		name, kind, header, body, parent, want string
	}{
		{name: "forked title", kind: "thread_title", header: `{"forked_from_thread_id":"main-a"}`, want: "main-a"},
		{name: "reconsidered title", kind: "thread_title_reconsideration", header: `{"parent_thread_id":"main-b"}`, want: "main-b"},
		{name: "ordinary fork stays separate", header: `{"forked_from_thread_id":"main-a"}`},
		{name: "other helper stays separate", kind: "guardian", parent: "main-a"},
		{name: "canonical body", kind: "thread_title", header: `{"forked_from_thread_id":"wrong"}`, body: `{"client_metadata":{"x-codex-turn-metadata":"{\"forked_from_thread_id\":\"main-b\"}"}}`, want: "main-b"},
		{name: "object metadata", kind: "thread_title", body: `{"client_metadata":{"x-codex-turn-metadata":{"parent_thread_id":"main-a"}}}`, want: "main-a"},
		{name: "parent header", kind: "thread_title", parent: "main-c", want: "main-c"},
		{name: "flat body", kind: "thread_title", body: `{"client_metadata":{"thread_source":"thread_title","x-codex-parent-thread-id":"main-flat"}}`, want: "main-flat"},
		{name: "partially malformed", kind: "thread_title", header: `{"parent_thread_id":"wrong","thread_source":7}`},
		{name: "malformed", kind: "thread_title", header: `{broken`},
		{name: "no ancestry", kind: "thread_title", header: `{"session_id":"aux","thread_id":"aux"}`},
		{name: "self reference", kind: "thread_title", parent: "aux"},
		{name: "overlong", kind: "thread_title", parent: strings.Repeat("x", 129)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("session_id", "aux")
			h.Set("x-codex-turn-metadata", tc.header)
			h.Set("x-codex-parent-thread-id", tc.parent)
			if got := titleParentSession(h, []byte(tc.body), tc.kind); got != tc.want {
				t.Fatalf("parent=%q, want %q", got, tc.want)
			}
			if sessionOf(h) != "aux" {
				t.Fatal("title grouping changed the request's routing identity")
			}
		})
	}
}

func TestRequestCallKindBody(t *testing.T) {
	meta, _ := json.Marshal(map[string]string{"thread_source": "thread_title", "forked_from_thread_id": "main"})
	body, _ := json.Marshal(map[string]any{"client_metadata": map[string]string{"x-codex-turn-metadata": string(meta)}})
	if got := requestCallKind(http.Header{}, body); got != "thread_title" {
		t.Fatalf("body-only title kind=%q", got)
	}
}
