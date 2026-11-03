package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/tool"
)

// ErrMaxDiscussionTurns is returned when the discussion has not ended before
// MaxTurns participant messages have been produced.
var ErrMaxDiscussionTurns = errors.New("agent: reached maximum discussion turns")

// DiscussionParticipant is one voice in a moderated multi-agent discussion.
// Each participant can use a different provider, model, system prompt, and set
// of tools. Tool-call turns are executed with tool.Node and returned to the
// participant model until it produces a text contribution.
type DiscussionParticipant struct {
	Name     string
	Provider bungkus.Provider
	Request  bungkus.ChatRequest
	Tools    []tool.Invokable
	MaxSteps int // tool/model calls per contribution; 0 means 5
}

// DiscussionCoordinator chooses who speaks next, or declares the goal complete.
// It receives the entire transcript on every turn. The response must be either
// the exact name of a participant or the exact completion token "DONE".
type DiscussionCoordinator struct {
	Provider bungkus.Provider
	Request  bungkus.ChatRequest
	Done     string // completion token; defaults to "DONE"
}

// Discussion runs a shared-transcript conversation with a coordinator and
// named participants. Normal participant turns are sequential. ParallelTurns
// optionally runs a coordinator-selected batch concurrently; those participants
// all see the same pre-round transcript and their contributions are appended in
// participant declaration order. This is suitable for independent perspectives,
// not for turns that must react to each other within a round.
//
// This is an experimental API and may change before v1.0.0.
type Discussion struct {
	Name          string
	Goal          string
	Coordinator   DiscussionCoordinator
	Participants  []DiscussionParticipant
	MaxTurns      int  // maximum participant messages; 0 means 12
	ParallelTurns bool // coordinator can select a comma-separated batch of speakers
}

// DiscussionResult contains the shared participant transcript, selected
// speaker names in turn order, coordinator/participant token usage, and the
// termination status. Coordinator messages are not included in Transcript.
type DiscussionResult struct {
	Transcript []bungkus.Message
	Speakers   []string
	Usage      bungkus.Usage
	Completed  bool // true only when the coordinator selected its Done token
}

const (
	defaultDiscussionTurns = 12
	defaultDiscussionSteps = 5
)

// Run moderates a discussion until the coordinator returns its completion
// token or the turn limit is reached. A turn-limit result is returned with a
// nil error and Completed=false so callers can inspect the partial transcript.
// Provider failures and invalid coordinator responses return an error along
// with the partial result.
func (d *Discussion) Run(ctx context.Context, initial []bungkus.Message) (result *DiscussionResult, err error) {
	result = &DiscussionResult{Transcript: append([]bungkus.Message(nil), initial...)}
	name := d.Name
	if name == "" {
		name = "discussion"
	}
	ctx, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentDiscussion, Name: name}, nil)
	defer func() { end(result, err) }()

	if d.Coordinator.Provider == nil {
		return result, errors.New("agent: discussion coordinator provider is required")
	}
	if len(d.Participants) == 0 {
		return result, errors.New("agent: discussion requires at least one participant")
	}
	if d.Goal == "" {
		return result, errors.New("agent: discussion goal is required")
	}
	participants := make(map[string]DiscussionParticipant, len(d.Participants))
	for _, p := range d.Participants {
		if p.Name == "" || p.Provider == nil {
			return result, errors.New("agent: each discussion participant needs a name and provider")
		}
		if _, exists := participants[p.Name]; exists {
			return result, fmt.Errorf("agent: duplicate discussion participant %q", p.Name)
		}
		if p.MaxSteps < 0 {
			return result, fmt.Errorf("agent: discussion participant %q MaxSteps cannot be negative", p.Name)
		}
		participants[p.Name] = p
	}
	if d.Coordinator.Done == "" {
		d.Coordinator.Done = "DONE"
	}
	maxTurns := d.MaxTurns
	if maxTurns == 0 {
		maxTurns = defaultDiscussionTurns
	}
	if maxTurns < 1 {
		return result, errors.New("agent: discussion MaxTurns must be positive")
	}

	for len(result.Speakers) < maxTurns {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		speakers, usage, err := d.nextSpeakers(ctx, result.Transcript, maxTurns-len(result.Speakers))
		result.Usage.Add(usage)
		if err != nil {
			return result, err
		}
		if len(speakers) == 1 && speakers[0] == d.Coordinator.Done {
			result.Completed = true
			return result, nil
		}
		if len(speakers) == 0 {
			return result, errors.New("agent: discussion coordinator selected no speakers")
		}
		if len(speakers) > maxTurns-len(result.Speakers) {
			return result, fmt.Errorf("agent: coordinator selected %d speakers beyond remaining turn limit", len(speakers))
		}
		batch := make([]DiscussionParticipant, len(speakers))
		for i, speaker := range speakers {
			p, ok := participants[speaker]
			if !ok {
				return result, fmt.Errorf("agent: coordinator selected unknown participant %q", speaker)
			}
			batch[i] = p
		}
		turns := make([]bungkus.Message, len(batch))
		usages := make([]bungkus.Usage, len(batch))
		errs := make([]error, len(batch))
		if d.ParallelTurns && len(batch) > 1 {
			var wg sync.WaitGroup
			for i, p := range batch {
				wg.Add(1)
				go func(i int, p DiscussionParticipant) {
					defer wg.Done()
					turns[i], usages[i], errs[i] = d.runParticipant(ctx, p, result.Transcript)
				}(i, p)
			}
			wg.Wait()
		} else {
			for i, p := range batch {
				turns[i], usages[i], errs[i] = d.runParticipant(ctx, p, result.Transcript)
				if errs[i] != nil {
					break
				}
			}
		}
		for i, turn := range turns {
			result.Usage.Add(usages[i])
			if errs[i] != nil {
				return result, fmt.Errorf("agent: discussion participant %q: %w", batch[i].Name, errs[i])
			}
			result.Transcript = append(result.Transcript, turn)
			result.Speakers = append(result.Speakers, batch[i].Name)
		}
	}
	return result, nil
}

func (d *Discussion) nextSpeakers(ctx context.Context, transcript []bungkus.Message, remaining int) ([]string, bungkus.Usage, error) {
	choices := make([]string, 0, len(d.Participants)+1)
	for _, p := range d.Participants {
		choices = append(choices, p.Name)
	}
	choices = append(choices, d.Coordinator.Done)
	instruction := "Choose exactly one next speaker"
	if d.ParallelTurns {
		instruction = "Choose one speaker, or a comma-separated batch of distinct speakers for independent parallel contributions"
	}
	msgs := append([]bungkus.Message(nil), d.Coordinator.Request.Messages...)
	msgs = append(msgs, bungkus.Message{Role: "user", Content: fmt.Sprintf(
		"Goal: %s\n\nDiscussion transcript:\n%s\n\n%s from: %s. Reply with only the selection. Reply %s only when the goal is complete; otherwise continue the discussion. At most %d participant turns remain.",
		d.Goal, renderTranscript(transcript), instruction, strings.Join(choices, ", "), d.Coordinator.Done, remaining)})
	resp, err := d.Coordinator.Provider.Chat(ctx, &bungkus.ChatRequest{
		Model: d.Coordinator.Request.Model, Messages: msgs, MaxTokens: d.Coordinator.Request.MaxTokens,
		Temperature: d.Coordinator.Request.Temperature, TopP: d.Coordinator.Request.TopP,
		Stop: d.Coordinator.Request.Stop, ReasoningEffort: d.Coordinator.Request.ReasoningEffort,
		ResponseFormat: d.Coordinator.Request.ResponseFormat,
	})
	if err != nil {
		return nil, respUsage(resp), fmt.Errorf("agent: discussion coordinator: %w", err)
	}
	if resp == nil {
		return nil, bungkus.Usage{}, errors.New("agent: discussion coordinator returned nil response")
	}
	answer := strings.TrimSpace(strings.Trim(resp.Content, "`"))
	if answer == d.Coordinator.Done {
		return []string{answer}, resp.Usage, nil
	}
	speakers := []string{answer}
	if d.ParallelTurns {
		speakers = strings.Split(answer, ",")
	}
	seen := make(map[string]bool, len(speakers))
	for i := range speakers {
		speakers[i] = strings.TrimSpace(speakers[i])
		if speakers[i] == "" || seen[speakers[i]] {
			return nil, resp.Usage, fmt.Errorf("agent: invalid coordinator speaker selection %q", answer)
		}
		seen[speakers[i]] = true
	}
	return speakers, resp.Usage, nil
}

func (d *Discussion) runParticipant(ctx context.Context, p DiscussionParticipant, transcript []bungkus.Message) (bungkus.Message, bungkus.Usage, error) {
	maxSteps := p.MaxSteps
	if maxSteps == 0 {
		maxSteps = defaultDiscussionSteps
	}
	messages := append([]bungkus.Message(nil), p.Request.Messages...)
	messages = append(messages, bungkus.Message{Role: "user", Content: discussionPrompt(d.Goal, p.Name, transcript)})
	node := tool.NewNode(p.Tools...)
	var usage bungkus.Usage
	for step := 0; step < maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return bungkus.Message{}, usage, err
		}
		req := p.Request
		req.Messages = messages
		req.Tools = append(append([]bungkus.Tool(nil), p.Request.Tools...), node.Infos()...)
		resp, err := p.Provider.Chat(ctx, &req)
		usage.Add(respUsage(resp))
		if err != nil {
			return bungkus.Message{}, usage, err
		}
		if resp == nil {
			return bungkus.Message{}, usage, errors.New("provider returned nil response")
		}
		messages = append(messages, resp.Message)
		if len(resp.ToolCalls) == 0 {
			return bungkus.Message{Role: "assistant", Content: "[" + p.Name + "] " + resp.Content}, usage, nil
		}
		results, err := node.Run(ctx, resp.Message)
		if err != nil {
			return bungkus.Message{}, usage, err
		}
		messages = append(messages, results...)
	}
	return bungkus.Message{}, usage, fmt.Errorf("tool loop exceeded %d model calls", maxSteps)
}

func discussionPrompt(goal, speaker string, transcript []bungkus.Message) string {
	return fmt.Sprintf("You are %s in a moderated multi-agent discussion.\nGoal: %s\n\nShared discussion transcript:\n%s\n\nContribute your perspective toward completing the goal. Be concise and build on prior contributions.", speaker, goal, renderTranscript(transcript))
}

func renderTranscript(transcript []bungkus.Message) string {
	var b strings.Builder
	for i, m := range transcript {
		role := m.Role
		content := m.Content
		if role == "assistant" {
			role = "participant"
			if speaker, rest, ok := strings.Cut(content, "] "); ok && strings.HasPrefix(speaker, "[") {
				role = strings.TrimPrefix(speaker, "[")
				content = rest
			}
		}
		fmt.Fprintf(&b, "%d. %s: %s\n", i+1, role, content)
	}
	if b.Len() == 0 {
		return "(discussion has not started)"
	}
	return b.String()
}

func respUsage(resp *bungkus.ChatResponse) bungkus.Usage {
	if resp == nil {
		return bungkus.Usage{}
	}
	return resp.Usage
}
