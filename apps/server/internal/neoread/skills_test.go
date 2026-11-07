package neoread

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTestSkills swaps the registry for the duration of one test. Serial tests
// never overlap with t.Parallel tests, so the package-level swap is safe.
func withTestSkills(t *testing.T, skills ...Skill) {
	t.Helper()
	old := builtinSkills
	builtinSkills = skills
	t.Cleanup(func() { builtinSkills = old })
}

var testSkill = Skill{Name: "deep-read", Description: "逐段拆解论文方法与证据", Body: "先读摘要和结论，再核对每个实验的变量与样本。"}

func TestNormalizeChatSkills(t *testing.T) {
	withTestSkills(t, testSkill)
	s := testServer(t)
	if out, e := s.normalizeChatSkills(nil); e != nil || len(out) != 0 {
		t.Fatalf("empty request: %v %v", out, e)
	}
	out, e := s.normalizeChatSkills([]string{"deep-read", "", "deep-read"})
	if e != nil || len(out) != 1 || out[0] != "deep-read" {
		t.Fatalf("dedupe: %v %v", out, e)
	}
	if _, e = s.normalizeChatSkills([]string{"no-such-skill"}); e == nil {
		t.Fatal("unknown skill accepted")
	}
	many := []string{"a", "b", "c", "d"}
	withTestSkills(t,
		Skill{Name: "a"}, Skill{Name: "b"}, Skill{Name: "c"}, Skill{Name: "d"},
	)
	if _, e = s.normalizeChatSkills(many); e == nil || !strings.Contains(e.Error(), "3") {
		t.Fatalf("cap: %v", e)
	}
}

func TestSkillInstructions(t *testing.T) {
	s := testServer(t)
	if got := s.skillInstructions("base", nil); got != "base" {
		t.Fatalf("no skills must not touch the base prompt: %q", got)
	}
	withTestSkills(t, testSkill)
	got := s.skillInstructions("base", []string{"deep-read"})
	for _, want := range []string{"base", "<selected_skills>", "先读摘要和结论", "不可信资料", "</selected_skills>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	// An unknown name is skipped rather than emitted as an empty section.
	if got = s.skillInstructions("", []string{"ghost"}); got != "" {
		t.Fatalf("unknown skill injected: %q", got)
	}
}

func TestUserSkillFiles(t *testing.T) {
	s := testServer(t)
	dir := filepath.Join(s.Store.Root, "skills")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(path, content string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(dir, path), []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("陪读.md", "---\nname: my-guide\ndescription: 我的陪读剧本\n---\n\n先提问再讲解。\n")
	// A frontmatter-less file is still a playbook: the filename becomes the
	// name and the whole file the body, so a plain export can be dropped in.
	write("plain-notes.md", "先查数据来源，再讲结论。\n")
	write("empty.md", "---\nname: empty-one\n---\n")
	if e := os.MkdirAll(filepath.Join(dir, "guide-pkg"), 0700); e != nil {
		t.Fatal(e)
	}
	write(filepath.Join("guide-pkg", "SKILL.md"), "---\nname: pkg-guide\ndescription: 目录形式\n---\n\n目录正文。\n")

	listed := s.userSkills()
	if len(listed) != 3 || listed[0].Name != "pkg-guide" || listed[1].Name != "plain-notes" || listed[2].Name != "my-guide" {
		t.Fatalf("user skills: %+v", listed)
	}
	if listed[1].Body != "先查数据来源，再讲结论。" || listed[1].Description != "" {
		t.Fatalf("plain file parse: %+v", listed[1])
	}
	if listed[2].Description != "我的陪读剧本" || !strings.Contains(listed[2].Body, "先提问") {
		t.Fatalf("frontmatter parse: %+v", listed[2])
	}
	// User skills resolve through the merged registry and inject their body.
	if _, ok := s.skillByName("my-guide"); !ok {
		t.Fatal("user skill not resolvable")
	}
	if got := s.skillInstructions("", []string{"my-guide"}); !strings.Contains(got, "先提问再讲解") {
		t.Fatalf("user body not injected: %q", got)
	}
	// A user skill shadows a same-named builtin.
	withTestSkills(t, testSkill)
	if e := os.WriteFile(filepath.Join(dir, "shadow.md"), []byte("---\nname: deep-read\ndescription: 自定义覆盖\n---\n\n自定义正文。\n"), 0600); e != nil {
		t.Fatal(e)
	}
	skill, ok := s.skillByName("deep-read")
	if !ok || skill.Body != "自定义正文。" {
		t.Fatalf("shadowing failed: %+v", skill)
	}
}

func TestSkillsEndpoint(t *testing.T) {
	s := testServer(t)
	var listed struct {
		Skills []Skill `json:"skills"`
	}
	if e := json.Unmarshal(request(t, s, "GET", "/api/skills", nil).Body.Bytes(), &listed); e != nil {
		t.Fatal(e)
	}
	if len(listed.Skills) != len(builtinSkills) || len(listed.Skills) == 0 {
		t.Fatalf("registry mismatch: %d listed, %d builtin", len(listed.Skills), len(builtinSkills))
	}
	names := map[string]bool{}
	for _, skill := range listed.Skills {
		if skill.Name == "" || skill.Description == "" {
			t.Fatalf("incomplete skill entry: %+v", skill)
		}
		if skill.Body != "" {
			t.Fatal("skill body must never leave the server")
		}
		names[skill.Name] = true
	}
	for _, want := range []string{"deep-read", "socratic", "review-lens", "stats-audit", "figures", "repro-check"} {
		if !names[want] {
			t.Fatalf("missing shipped skill: %s", want)
		}
	}
}

// The API document path must fold selected skills into the system message and
// persist the selection on the user message for retry parity.
func TestAPIDocumentChatSkillsInjection(t *testing.T) {
	withTestSkills(t, testSkill)
	var system string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []toolCompletionMessage `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) > 0 && body.Messages[0].Role == "system" {
			if content, ok := body.Messages[0].Content.(string); ok {
				system = content
			}
		}
		writeAPIReply(w, "Original nonce [S2]")
	}))
	defer upstream.Close()
	_, a := originalDocumentFixture(t)
	in := AIInput{Prompt: "COBALT-19", Chat: &chatInput{Question: "COBALT-19", Skills: []string{"deep-read"}, Agent: a}}
	var text strings.Builder
	_, e := streamAPIDocumentChat(t.Context(), APIConnection{URL: upstream.URL}, completionRequest{Model: "plain"}, in, func(v ProviderEvent) error { text.WriteString(v.Text); return nil })
	if e != nil || text.Len() == 0 {
		t.Fatalf("chat failed: %v", e)
	}
	if !strings.Contains(system, "先读摘要和结论") || !strings.Contains(system, "不可信资料") {
		t.Fatalf("skill body missing from system prompt: %q", system)
	}
	// A turn without skills keeps the plain instructions.
	system = ""
	in2 := AIInput{Prompt: "COBALT-19", Chat: &chatInput{Question: "COBALT-19", Agent: a}}
	if _, e = streamAPIDocumentChat(t.Context(), APIConnection{URL: upstream.URL}, completionRequest{Model: "plain"}, in2, func(v ProviderEvent) error { text.WriteString(v.Text); return nil }); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(system, "<selected_skills>") {
		t.Fatalf("skills leaked without selection: %q", system)
	}
}

func TestSkillTurnPrefix(t *testing.T) {
	s := testServer(t)
	withTestSkills(t, testSkill)
	if got := s.skillTurnPrefix(nil, nil); got != "" {
		t.Fatalf("unchanged empty set must stay silent: %q", got)
	}
	if got := s.skillTurnPrefix([]string{"deep-read"}, []string{"deep-read"}); got != "" {
		t.Fatalf("unchanged set must stay silent: %q", got)
	}
	selected := s.skillTurnPrefix(nil, []string{"deep-read"})
	if !strings.Contains(selected, "先读摘要和结论") {
		t.Fatalf("selection not injected: %q", selected)
	}
	cleared := s.skillTurnPrefix([]string{"deep-read"}, nil)
	if cleared != skillRetraction || !strings.Contains(cleared, "不再适用") {
		t.Fatalf("clearing skills must revoke them explicitly: %q", cleared)
	}
	swapped := s.skillTurnPrefix([]string{"ghost"}, []string{"deep-read"})
	if !strings.Contains(swapped, "先读摘要和结论") {
		t.Fatalf("swap must inject the new set: %q", swapped)
	}
}

func TestSameChatQuestionCoversSkills(t *testing.T) {
	base := Message{Role: "user", Content: "q", Skills: []string{"a"}}
	if sameChatQuestion(base, Message{Role: "user", Content: "q", Skills: []string{"a"}}) != true {
		t.Fatal("identical skills diverged")
	}
	if sameChatQuestion(base, Message{Role: "user", Content: "q"}) {
		t.Fatal("missing skills accepted")
	}
	if sameChatQuestion(base, Message{Role: "user", Content: "q", Skills: []string{"b"}}) {
		t.Fatal("changed skills accepted")
	}
}
