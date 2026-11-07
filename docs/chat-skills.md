# Chat skills

A chat skill is a named, trusted playbook the reader can attach to one
question. It is not a document source: skill text never enters the untrusted
data path, and document, tool and history content still cannot issue
instructions.

## Contract

- `GET /api/skills` returns `{"skills": [{"name": "...", "description": "..."}]}`.
  The body stays on the server; only name and description reach the renderer.
- A chat request may carry `skills: string[]`. Unknown names are rejected with
  400; at most three names apply per turn, and duplicates collapse.
- The selection is stored on the user message (`Message.Skills`), so
  regeneration replays it. `sameChatQuestion` treats a changed set as a
  different question and refuses the retry.
- Selected bodies are appended to the turn's instructions as
  `<selected_skills>`: the API system message, the pi system prompt, the Codex
  turn input when the applied set changes, and the legacy CLI prompt. A Codex
  chat session with a different set is discarded rather than reused.

## Shipped playbooks

The registry ships six reading-companion skills. Each body is an original
Chinese playbook written for NeoRead's read-only chat context (evidence
citations via `[S#]`, document content stays untrusted data); where an
external skill inspired the structure, the idea was adapted, not copied.

| Name | Purpose | Primary inspiration |
|---|---|---|
| `deep-read` | causal-chain walkthrough: motivation → pipeline → real contribution → evidence bounds | [zhou2030109-glitch/paper-reading](https://github.com/zhou2030109-glitch/paper-reading); the local `nature-paper-card` claim-evidence matrix |
| `socratic` | guided companion: checks understanding with questions before advancing | [oldlybaby/paper-reading-guide](https://github.com/oldlybaby/paper-reading-guide) (concept) |
| `review-lens` | single-reviewer assessment with anchored Major/Minor concerns | the local `nature-reviewer` skill, degraded to one pass |
| `stats-audit` | n/replication/uncertainty/check audit of methods and figures | the local `nature-statistics` skill |
| `figures` | per-figure guided reading with a worked example | `paper-reading` figure section |
| `repro-check` | reproduction checklist with explicit missing-detail markers | the local `paper-lab` inventory idea |

## Adding a skill

Add one entry to `builtinSkills` in `apps/server/internal/neoread/skills.go`:

```go
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"-"`
}
```

- `Name` is the wire identifier: lowercase words joined by hyphens.
- `Description` is what the picker shows under the name; keep it one line and
  concrete, because it is the only routing hint the reader sees.
- `Body` is the playbook. State the procedure, the evidence rules and the
  output shape. It rides outside the compaction window, so keep it tight:
  every selected skill is paid for on every turn.
- Skills are instructions for the assistant, not for the document. Say what to
  do with evidence, and leave the untrusted-data boundary to the shared
  preamble.

No other change is required: the picker, validation, persistence and provider
injection all read the registry.

## Verification

`go test ./internal/neoread/ -run Skill` covers validation, the empty
registry, prompt injection and retry parity. Add a skill and run it again; the
API injection test uses its own temporary registry, so it does not depend on
shipped skills.
