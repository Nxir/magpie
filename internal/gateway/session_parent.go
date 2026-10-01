package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

func isTitleKind(kind string) bool {
	return kind == "thread_title" || kind == "thread_title_reconsideration" || kind == "title_generation"
}

// Some desktop titles start fresh ephemeral threads without ancestry. Their
// user message contains the exact original prompt after this template marker.
// Retain only its digest, and match only when one observed chat has that prompt.
// This does not use account, model or temporal proximity as identity evidence.
func sessionPromptKey(body []byte, kind string) string {
	var request struct {
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(body, &request) != nil {
		return ""
	}
	var items []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(request.Input, &items) != nil {
		return ""
	}
	for _, item := range items {
		if item.Role != "user" {
			continue
		}
		var text string
		if json.Unmarshal(item.Content, &text) != nil {
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(item.Content, &parts) != nil {
				continue
			}
			for _, part := range parts {
				if part.Type == "input_text" || part.Type == "text" {
					text += part.Text
				}
			}
		}
		text = strings.TrimSpace(text)
		if isTitleKind(kind) {
			// Recognize the actual desktop template, not arbitrary quoted text.
			if !strings.HasPrefix(text, "You are a helpful assistant. You will be presented with a user prompt,") {
				continue
			}
			_, prompt, ok := strings.Cut(text, "\n\nUser prompt:\n")
			if !ok {
				continue
			}
			text = strings.TrimSpace(prompt)
		} else if kind != "" || strings.HasPrefix(text, "<environment_context>") || strings.HasPrefix(text, "<user_instructions>") || strings.HasPrefix(text, "<external_codex_apps_open_page>") {
			continue
		}
		if text == "" {
			continue
		}
		digest := sha256.Sum256([]byte(text))
		return hex.EncodeToString(digest[:])
	}
	return ""
}

// Keep collision evidence beyond the live trace's 60 requests. Once this
// bounded index fills, decline inference until restart instead of dropping
// an older candidate and mistaking a repeated prompt for a unique one.
const maxTitlePromptKeys = 4096

type titleParents struct {
	sessions map[string]string // digest -> unique session; empty means ambiguous
	full     bool
}

func (p *titleParents) observe(r Route) {
	if p.full || r.Agent != "codex" || r.Kind != "" || r.Session == "" || r.PromptKey == "" {
		return
	}
	if p.sessions == nil {
		p.sessions = map[string]string{}
	}
	if previous, ok := p.sessions[r.PromptKey]; ok {
		if previous != r.Session {
			p.sessions[r.PromptKey] = ""
		}
		return
	}
	if len(p.sessions) >= maxTitlePromptKeys {
		p.sessions, p.full = nil, true
		return
	}
	p.sessions[r.PromptKey] = r.Session
}

func (p *titleParents) resolve(r *Route) {
	if r.Agent != "codex" || !isTitleKind(r.Kind) || r.PromptKey == "" || (r.ParentSession != "" && !r.ParentMatched) {
		return
	}
	r.ParentSession, r.ParentMatched = "", false
	if !p.full {
		if id := p.sessions[r.PromptKey]; id != "" && id != r.Session {
			r.ParentSession, r.ParentMatched = id, true
		}
	}
}

// ResolveTitleParents uses all retained records of a day before the display
// limit is applied. A recorded inference is also candidate evidence, so
// absence of its original request never reassigns it to a different chat.
func ResolveTitleParents(routes []Route) []Route {
	out := append([]Route(nil), routes...)
	var parents titleParents
	for _, r := range out {
		parents.observe(r)
		if r.ParentMatched && r.ParentSession != "" {
			r.Session, r.Kind = r.ParentSession, ""
			parents.observe(r)
		}
	}
	for i := range out {
		parents.resolve(&out[i])
	}
	return out
}

// Codex projects turn metadata into headers, but its canonical transport is
// client_metadata in the Responses body. Read only identity fields; never keep
// prompts or the rest of the metadata in the trace.
type sessionMetadata struct {
	Source string `json:"thread_source"`
	Parent string `json:"parent_thread_id"`
	Forked string `json:"forked_from_thread_id"`
}

func requestSessionMetadata(h http.Header, body []byte) sessionMetadata {
	var envelope struct {
		Metadata map[string]json.RawMessage `json:"client_metadata"`
	}
	var m sessionMetadata
	if json.Unmarshal(body, &envelope) == nil {
		if raw := envelope.Metadata["x-codex-turn-metadata"]; len(raw) > 0 {
			var text string
			if json.Unmarshal(raw, &text) == nil {
				raw = []byte(text)
			}
			if json.Unmarshal(raw, &m) == nil {
				return m
			}
		} else if len(envelope.Metadata) > 0 {
			// Compatibility projections used by clients without the full blob.
			_ = json.Unmarshal(envelope.Metadata["thread_source"], &m.Source)
			_ = json.Unmarshal(envelope.Metadata["parent_thread_id"], &m.Parent)
			if m.Parent == "" {
				_ = json.Unmarshal(envelope.Metadata["x-codex-parent-thread-id"], &m.Parent)
			}
			_ = json.Unmarshal(envelope.Metadata["forked_from_thread_id"], &m.Forked)
			if m.Source != "" || m.Parent != "" || m.Forked != "" {
				return m
			}
		}
	}
	m = sessionMetadata{}
	if json.Unmarshal([]byte(h.Get("x-codex-turn-metadata")), &m) != nil {
		return sessionMetadata{}
	}
	return m
}

func requestCallKind(h http.Header, body []byte) string {
	if kind := callKind(h); h.Get("x-openai-subagent") != "" || h.Get("x-openai-memgen-request") != "" {
		return kind
	}
	m := requestSessionMetadata(h, body)
	switch source := strings.TrimSpace(m.Source); source {
	case "":
		return callKind(h)
	case "user", "subagent":
		return ""
	default:
		if len(source) > 40 {
			source = source[:40]
		}
		return source
	}
}

// Only title helpers inherit their originating chat. A user-created fork is a
// separate conversation, even though it carries the same fork ancestry.
func titleParentSession(h http.Header, body []byte, kind string) string {
	if !isTitleKind(kind) {
		return ""
	}
	m := requestSessionMetadata(h, body)
	for _, id := range []string{m.Parent, m.Forked, h.Get("x-codex-parent-thread-id")} {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 128 || strings.ContainsAny(id, "\r\n\t") || id == sessionOf(h) {
			continue
		}
		return id
	}
	return ""
}
