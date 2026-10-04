package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/sessions"
)

// TitleLink contains only fingerprints. Native request IDs and account affinity
// are never changed by an inferred display association.
type TitleLink struct {
	Scope  string `json:"scope"`
	Prompt string `json:"prompt"`
	Reply  string `json:"reply,omitempty"`
}

type titlePrompt struct {
	Session string
	Time    time.Time
	Link    TitleLink
}
type titlePrompts struct {
	sync.Mutex
	first    map[string]titlePrompt
	byPrompt map[string][]titlePrompt
	overflow bool
}

const titlePromptLimit = 4096

func promptDigest(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:])
}

// Inspect only the first real user message, before any assistant reply. A
// multimodal or unfamiliar title template stays unassociated. GetBytes stops
// at the selected item, avoiding a copy of the rest of a long conversation.
func titlePromptDigest(body []byte, title bool) string {
	for i := 0; i < 32; i++ {
		item := gjson.GetBytes(body, "input."+strconv.Itoa(i))
		if !item.Exists() {
			return ""
		}
		if item.Get("role").String() == "assistant" {
			return ""
		}
		if item.Get("role").String() != "user" {
			continue
		}
		content := item.Get("content")
		text := ""
		if content.Type == gjson.String {
			text = content.String()
		} else if content.IsArray() {
			valid := true
			content.ForEach(func(_, part gjson.Result) bool {
				if part.Get("type").String() != "input_text" {
					valid = false
					return false
				}
				text += part.Get("text").String()
				return true
			})
			if !valid {
				return ""
			}
		}
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, "<environment_context>") || strings.HasPrefix(text, "<user_instructions>") || strings.HasPrefix(text, "# AGENTS.md instructions for ") || strings.HasPrefix(text, "<external_codex_apps_open_page>") {
			continue
		}
		if title {
			const prefix = "You are a helpful assistant. You will be presented with a user prompt,"
			const marker = "\n\nUser prompt:\n"
			if !strings.HasPrefix(text, prefix) {
				return ""
			}
			_, tail, ok := strings.Cut(text, marker)
			if !ok {
				return ""
			}
			text = strings.TrimSpace(tail)
		}
		if text == "" {
			return ""
		}
		return promptDigest(text)
	}
	return ""
}

func (p *titlePrompts) observe(r *http.Request, body []byte, m sessionMetadata, kind string, at time.Time) *TitleLink {
	if agentOf(r) != "codex" || !local(r) || callerOf(r).via != "" || m.Installation == "" || len(m.Installation) > 128 || m.Source == "subagent" || (kind != "" && !isTitleKind(kind)) {
		return nil
	}
	id := sessionOf(r.Header)
	if id == "" {
		return nil
	}
	scope := promptDigest(m.Installation)
	if isTitleKind(kind) {
		if key := titlePromptDigest(body, true); key != "" {
			return &TitleLink{Scope: scope, Prompt: key}
		}
		return nil
	}
	p.Lock()
	defer p.Unlock()
	key := scope + ":" + id
	if _, seen := p.first[key]; seen {
		return nil
	}
	if len(p.first) >= titlePromptLimit {
		p.overflow = true
		return nil
	}
	if p.first == nil {
		p.first = make(map[string]titlePrompt)
	}
	link := TitleLink{Scope: scope, Prompt: titlePromptDigest(body, false)}
	p.first[key] = titlePrompt{Session: id, Time: at, Link: link}
	if link.Prompt == "" {
		return nil
	}
	if p.byPrompt == nil {
		p.byPrompt = make(map[string][]titlePrompt)
	}
	p.byPrompt[scope+":"+link.Prompt] = append(p.byPrompt[scope+":"+link.Prompt], p.first[key])
	return &link
}

func titleReplyDigest(body []byte, wrapped bool) string {
	if !gjson.ValidBytes(body) {
		completed := false
		readSSE(strings.NewReader(string(body)), func(_, data string) error {
			switch gjson.Get(data, "type").String() {
			case "response.completed":
				completed = true
			case "response.incomplete", "response.failed", "error":
				completed = false
			}
			return nil
		})
		if !completed {
			return ""
		}
	} else if status := gjson.GetBytes(body, "status").String(); status != "" && status != "completed" {
		return ""
	}
	res, err := compactReply(body)
	if err != nil || res.Error != nil {
		return ""
	}
	text := messageText(res)
	if wrapped {
		text = titleJSON(text)
	} // exactly the title handed to Codex

	// A native reply must really contain the structured title Codex accepts.
	if !gjson.Valid(text) {
		return ""
	}
	title := gjson.Get(text, "title")
	if title.Type != gjson.String {
		return ""
	}
	return sessions.CodexTitleDigest(title.String())
}

// ResolveTitleParents checks completed title replies against actual Codex name
// writes. It operates on copies for the GUI only, never on gateway routing.
// An explicit parent wins. Missing, conflicting or old evidence fails closed.
func (s *Server) ResolveTitleParents(rows []Route) []Route {
	needed := false
	for _, r := range rows {
		if isTitleKind(r.Kind) && r.TitleLink != nil {
			needed = true
			break
		}
	}
	if !needed {
		return rows
	}
	s.titlePrompts.Lock()
	if s.titlePrompts.overflow {
		s.titlePrompts.Unlock()
		return clearInferredParents(rows)
	}
	first := []titlePrompt{}
	seen := map[string]bool{}
	for _, r := range rows {
		if !isTitleKind(r.Kind) || r.TitleLink == nil {
			continue
		}
		key := r.TitleLink.Scope + ":" + r.TitleLink.Prompt
		if !seen[key] {
			first = append(first, s.titlePrompts.byPrompt[key]...)
			seen[key] = true
		}
	}
	s.titlePrompts.Unlock()
	return resolveTitleParents(rows, first, sessions.CodexTitleRecipients)
}

func resolveTitleParents(rows []Route, first []titlePrompt, applications func([]string) map[string][]sessions.TitleApplication) []Route {
	out := clearInferredParents(rows)
	byPrompt := map[string]map[string]time.Time{}
	add := func(p titlePrompt) {
		if p.Link.Prompt == "" || p.Link.Scope == "" || p.Session == "" {
			return
		}
		key := p.Link.Scope + ":" + p.Link.Prompt
		if byPrompt[key] == nil {
			byPrompt[key] = map[string]time.Time{}
		}
		old, ok := byPrompt[key][p.Session]
		if !ok || p.Time.Before(old) {
			byPrompt[key][p.Session] = p.Time
		}
	}
	for _, p := range first {
		add(p)
	}
	for _, r := range rows {
		if r.Agent == "codex" && r.Kind == "" && r.TitleLink != nil {
			add(titlePrompt{r.Session, r.Time, *r.TitleLink})
		}
	}
	digests := map[string]bool{}
	for _, r := range out {
		if r.Agent != "codex" || !isTitleKind(r.Kind) || r.TitleLink == nil || r.TitleLink.Reply == "" || !r.Done || r.Status >= 400 || r.Error != "" {
			continue
		}
		digests[r.TitleLink.Reply] = true
	}
	if len(digests) == 0 {
		return out
	}
	want := make([]string, 0, len(digests))
	for digest := range digests {
		want = append(want, digest)
	}
	applied := applications(want)
	for i := range out {
		r := &out[i]
		if r.ParentSession != "" || r.Agent != "codex" || !isTitleKind(r.Kind) || r.TitleLink == nil || r.TitleLink.Reply == "" || !r.Done || r.Status >= 400 || r.Error != "" {
			continue
		}
		parent := ""
		// The name write must follow this request and promptly follow its reply.
		// Time is corroborating evidence, never the sole matching key.
		end := r.Time.Add(time.Duration(r.Millis)*time.Millisecond + 2*time.Minute)
		for id, writes := range applied {
			for _, a := range writes {
				if a.Digest != r.TitleLink.Reply || a.Updated.Before(r.Time) || a.Updated.After(end) {
					continue
				}
				if parent != "" && parent != id {
					parent = "!ambiguous"
					break
				}
				parent = id
				break
			}
			if parent == "!ambiguous" {
				break
			}
		}
		at, known := byPrompt[r.TitleLink.Scope+":"+r.TitleLink.Prompt][parent]
		if parent != "" && parent != "!ambiguous" && parent != r.Session && known && !at.After(end) {
			r.ParentSession, r.ParentMatched = parent, true
		}
	}
	return out
}

func clearInferredParents(rows []Route) []Route {
	out := append([]Route{}, rows...)
	for i := range out {
		if out[i].ParentMatched {
			out[i].ParentSession, out[i].ParentMatched = "", false
		}
	}
	return out
}
