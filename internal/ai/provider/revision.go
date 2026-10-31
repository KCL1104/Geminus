package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// One repair after the server's check (H-057, under H-055's stability
// decision): when the server refuses a proposal's first answer for what it
// says (the client's invalid_output with the model's own answer in hand, or
// the server's review), the job asks the model once more, and the review of
// that second answer is the proposal's outcome. The second request is the
// first one's sealed prompt again (the same system text, blocks and images),
// the model's previous answer as the assistant turn after it, and a user
// turn of the server's (Revision.User: the versioned repair template and the
// server's issues, cleaned, in a server_data block; internal/ai/aicontext
// Revise). It goes through the same client as the first: checked, metered
// (its own reservation before it is sent) and answered under the same
// schema. A revision is never researched: the repair of a research
// proposal keeps research's output bound and timeouts (Revision.Researched)
// but has no web search.
//
// The previous answer is untrusted text the model wrote. It is held in
// memory for that one call (PriorAnswer): never stored, logged, printed or
// put in an error; only a backend reads its bytes, to send them back as the
// model's own turn. Its digest, not its text, is sealed into the second
// prompt's InputHash.

// MaxRevisionBytes bounds a revision's follow-up turn (Revision.User): the
// repair template and the issues it lists. The prior answer is bounded by
// the budget's MaxOutputBytes, as the client took it.
const MaxRevisionBytes = 64 << 10

// priorWithheld is what a PriorAnswer prints as.
const priorWithheld = "[prior answer withheld]"

// PriorAnswer is a model's previous answer as its backend took it
// (Response.Answer: the text before the backend decoded it), held for one
// repair (H-057). Only the client makes one (ProposalResult.Prior). It
// prints, formats and encodes as withheld, whatever the verb: its bytes
// leave it only through Bytes.
type PriorAnswer struct{ text []byte }

// Present reports whether there is an answer to send back.
func (a PriorAnswer) Present() bool { return len(a.text) > 0 }

// Len is the answer's size in bytes.
func (a PriorAnswer) Len() int { return len(a.text) }

// Bytes is a copy of the answer, for a backend to send as the model's own
// turn. Never log or store it.
func (a PriorAnswer) Bytes() []byte { return bytes.Clone(a.text) }

func (a PriorAnswer) digest() string {
	sum := sha256.Sum256(a.text)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// String, GoString and Format print the answer as withheld.
func (PriorAnswer) String() string   { return priorWithheld }
func (PriorAnswer) GoString() string { return priorWithheld }

func (PriorAnswer) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(priorWithheld)) }

// MarshalJSON encodes the answer as null: a struct that holds one never
// carries the text into JSON.
func (PriorAnswer) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// Revision is the second turn of a repair (H-057): the model's prior answer,
// sent back as its own (assistant) turn, then the server's follow-up, a user
// turn of Rules and Blocks.
type Revision struct {
	Prior PriorAnswer
	// Rules is the repair template (prompts/repair, its version in the
	// prompt's TemplateVersion): trusted server text, never data.
	Rules string
	// Blocks are the follow-up's data: server_data only (SERVER_REVIEW: the
	// server's issues, each a code, a cleaned path and a fixed sentence of
	// the server's).
	Blocks []Block
	// Researched: the first call researched (H-046 (2)). The repair keeps a
	// research call's output bound and timeouts, never its web search.
	Researched bool
}

// User renders the follow-up's user turn: the rules, then each block as the
// first turn renders them.
func (r Revision) User() string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(r.Rules, "\n"))
	b.WriteString("\n")
	for _, blk := range r.Blocks {
		fmt.Fprintf(&b, "<%s name=\"%s\">\n%s\n</%s>\n", blk.Kind, blk.Name, blk.Content, blk.Kind)
	}
	return b.String()
}

// revisionRef is what the hash says of a revision: the prior answer's
// digest (never its text), the follow-up as sent, and whether the first
// call researched.
type revisionRef struct {
	Prior      string `json:"prior_digest"`
	User       string `json:"user"`
	Researched bool   `json:"researched,omitzero"`
}

func (r *Revision) ref() *revisionRef {
	if r == nil {
		return nil
	}
	return &revisionRef{Prior: r.Prior.digest(), User: r.User(), Researched: r.Researched}
}

// holdsBlock reports whether text holds a block's opening or closing tag:
// data interpolated into rules would be read as rules.
func holdsBlock(text string) bool {
	for _, k := range []BlockKind{UntrustedData, ServerData} {
		if strings.Contains(text, "<"+string(k)+" name=") || strings.Contains(text, "</"+string(k)+">") {
			return true
		}
	}
	return false
}

// sealRevision checks r and returns its sealed copy: the prior answer's own
// bytes and the blocks re-encoded as a prompt's are.
func sealRevision(r *Revision, p Prompt) (*Revision, error) {
	if r == nil {
		return nil, nil
	}
	fail := func(format string, a ...any) (*Revision, error) {
		return nil, fmt.Errorf("%w: the revision: "+format, append([]any{ErrInvalidPrompt}, a...)...)
	}
	switch {
	case !p.Role.Researches():
		return fail("only a proposal role's prompt is revised")
	case p.Research:
		return fail("a revision is never researched")
	case !r.Prior.Present():
		return fail("no prior answer")
	case strings.TrimSpace(r.Rules) == "":
		return fail("no rules")
	case holdsBlock(r.Rules):
		return fail("the rules contain a block")
	case len(r.Blocks) == 0:
		return fail("no blocks")
	}
	out := &Revision{Prior: PriorAnswer{text: bytes.Clone(r.Prior.text)}, Rules: r.Rules, Researched: r.Researched, Blocks: make([]Block, len(r.Blocks))}
	names := map[string]bool{}
	for i, b := range r.Blocks {
		if b.Kind != ServerData {
			return fail("block %d: the follow-up holds server data only", i)
		}
		if !blockName.MatchString(b.Name) || names[b.Name] {
			return fail("block %d: bad or repeated name", i)
		}
		names[b.Name] = true
		c, err := canonicalContent(b.Content)
		if err != nil {
			return fail("block %s: content is not one strict JSON value", b.Name)
		}
		out.Blocks[i] = Block{Kind: b.Kind, Name: b.Name, Content: c}
	}
	return out, nil
}

// revisionsEqual reports whether two sealed revisions are the same.
func revisionsEqual(a, b *Revision) bool {
	if a == nil || b == nil {
		return a == b
	}
	return bytes.Equal(a.Prior.text, b.Prior.text) && a.Rules == b.Rules && a.Researched == b.Researched && blocksEqual(a.Blocks, b.Blocks)
}
