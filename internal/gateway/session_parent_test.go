package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestTitleWithoutAncestryMatchesExactPrompt(t *testing.T) {
	body := func(text string) []byte {
		b, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": text}}}}})
		return b
	}
	prompt := "查询北京天气"
	mainKey := sessionPromptKey(body(prompt+"\n"), "")
	contextual, _ := json.Marshal(map[string]any{"input": []any{
		map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": "<environment_context>cwd and sandbox</environment_context>"}}},
		map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": "<external_codex_apps_open_page>{\"page_id\":null}</external_codex_apps_open_page>"}}},
		map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": prompt + "\n"}}},
	}})
	if sessionPromptKey(contextual, "") != mainKey {
		t.Fatal("desktop context prelude hid the actual prompt")
	}

	titleKey := sessionPromptKey(body("You are a helpful assistant. You will be presented with a user prompt, and your job is to provide a short title.\n\nUser prompt:\n"+prompt), "thread_title")
	if mainKey == "" || mainKey != titleKey {
		t.Fatal("exact prompt did not match")
	}
	if sessionPromptKey(body("unrecognized template\n\nUser prompt:\n"+prompt), "thread_title") != "" {
		t.Fatal("accepted unknown title template")
	}
	if sessionPromptKey(body("<environment_context>private context</environment_context>"), "") != "" {
		t.Fatal("environment treated as user prompt")
	}
	if sessionPromptKey(body("查询长沙天气"), "") == mainKey {
		t.Fatal("different prompts matched")
	}
	rows := []Route{
		{Agent: "codex", Session: "hidden", Kind: "thread_title", PromptKey: titleKey},
		{Agent: "codex", Session: "beijing", PromptKey: mainKey},
		{Agent: "codex", Session: "beijing", PromptKey: mainKey},
		{Agent: "claude", Session: "unrelated", PromptKey: mainKey},
	}
	got := ResolveTitleParents(rows)
	if got[0].ParentSession != "beijing" || !got[0].ParentMatched {
		t.Fatalf("parent: %+v", got[0])
	}
	got = ResolveTitleParents(append(got, Route{Agent: "codex", Session: "second", PromptKey: mainKey}))
	if got[0].ParentSession != "" || got[0].ParentMatched {
		t.Fatal("ambiguous duplicate prompt assigned to one chat")
	}
	rows[0].ParentSession = "explicit"
	if ResolveTitleParents(rows)[0].ParentSession != "explicit" {
		t.Fatal("explicit parent replaced")
	}
	// Title-first delivery must issue a changed title row through incremental
	// trace polling, so the browser can move an already-visible separate group.
	s := New()
	s.trace.begin(Route{Agent: "codex", Session: "hidden", Kind: "thread_title", PromptKey: titleKey})
	seq := s.Trace(context.Background(), 0, 0).Seq
	s.trace.begin(Route{Agent: "codex", Session: "beijing", PromptKey: mainKey})
	changed := s.Trace(context.Background(), seq, 0)
	if len(changed.Routes) != 2 || changed.Routes[0].ParentSession != "beijing" {
		t.Fatalf("late parent not emitted: %+v", changed.Routes)
	}
	seq = changed.Seq
	s.trace.begin(Route{Agent: "codex", Session: "second", PromptKey: mainKey})
	changed = s.Trace(context.Background(), seq, 0)
	if len(changed.Routes) != 2 || changed.Routes[0].ParentSession != "" {
		t.Fatal("late ambiguity did not revoke match")
	}
}

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

func TestTitleMatchingKeepsCollisionEvidenceAfterTraceEviction(t *testing.T) {
	s := New()
	s.trace.begin(Route{Agent: "codex", Session: "first", PromptKey: "digest"})
	for range traceKeep {
		s.trace.begin(Route{Agent: "other"})
	}
	title := s.trace.begin(Route{Agent: "codex", Session: "helper", Kind: "thread_title", PromptKey: "digest"})
	if title.ParentSession != "first" {
		t.Fatal("eviction lost the original candidate")
	}
	s.trace.begin(Route{Agent: "codex", Session: "second", PromptKey: "digest"})
	if title.ParentSession != "" {
		t.Fatal("a duplicate prompt did not revoke the match")
	}
	for range traceKeep {
		s.trace.begin(Route{Agent: "other"})
	}
	title = s.trace.begin(Route{Agent: "codex", Session: "later-helper", Kind: "thread_title", PromptKey: "digest"})
	if title.ParentSession != "" {
		t.Fatal("eviction made an ambiguous prompt unique again")
	}
}

func TestTitleMatchingStopsAtBound(t *testing.T) {
	var parents titleParents
	for i := range maxTitlePromptKeys + 1 {
		parents.observe(Route{Agent: "codex", Session: "main", PromptKey: strconv.Itoa(i)})
	}
	r := Route{Agent: "codex", Session: "helper", Kind: "thread_title", PromptKey: "0", ParentSession: "main", ParentMatched: true}
	parents.resolve(&r)
	if !parents.full || parents.sessions != nil || r.ParentSession != "" {
		t.Fatal("full index retained an unsafe inference")
	}
	r.ParentSession, r.ParentMatched = "explicit", false
	parents.resolve(&r)
	if r.ParentSession != "explicit" {
		t.Fatal("index bound changed explicit ancestry")
	}
}

func TestRequestCallKindBody(t *testing.T) {
	meta, _ := json.Marshal(map[string]string{"thread_source": "thread_title", "forked_from_thread_id": "main"})
	body, _ := json.Marshal(map[string]any{"client_metadata": map[string]string{"x-codex-turn-metadata": string(meta)}})
	if got := requestCallKind(http.Header{}, body); got != "thread_title" {
		t.Fatalf("body-only title kind=%q", got)
	}
}
