// Package agent runs models in a loop with tools.
package agent

import (
	"context"
	"errors"
	"iter"
	"slices"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/tool"
)

// ErrMaxSteps is returned when the model still wants tools after MaxSteps calls.
var ErrMaxSteps = errors.New("agent: reached MaxSteps without a final answer")

// ReAct calls the model, runs the tools it asks for, feeds the results back,
// and repeats until the model answers without calling a tool.
type ReAct struct {
	Name     string // reported to callbacks; "" means "react"
	Provider bungkus.Provider
	// Request is the template for every model call: set Model, MaxTokens,
	// Temperature and so on. Its Messages (e.g. a system prompt) go before
	// the conversation passed to Run. Tools are added to its Tools.
	Request  bungkus.ChatRequest
	Tools    []tool.Invokable
	MaxSteps int // maximum model calls per Run; 0 means 10
}

type Result struct {
	// Messages are the turns this run added: assistant tool calls, tool
	// results, and the final answer last. Append them to your history.
	Messages []bungkus.Message
	Usage    bungkus.Usage // summed over all model calls
}

// Answer is the final assistant text.
func (r *Result) Answer() string {
	if len(r.Messages) == 0 {
		return ""
	}
	return r.Messages[len(r.Messages)-1].Content
}

// Event is one step of a streamed run. Exactly one field is set.
type Event struct {
	Text      string           // a delta of the model's text
	Reasoning string           // a delta of the model's reasoning
	Message   *bungkus.Message // a finished turn: the assistant's message (maybe with ToolCalls) or a tool result
	Result    *Result          // the whole run; always the last event
}

// Run continues the conversation in history until the model gives a final
// answer. On error, the Result holds the turns completed so far.
func (a *ReAct) Run(ctx context.Context, history []bungkus.Message) (*Result, error) {
	return a.loop(ctx, history, a.Provider.Chat, func(*Event) bool { return true })
}

// Stream runs like Run but yields text and reasoning deltas as the model
// writes them, and each finished turn. Stop ranging to cancel the run.
func (a *ReAct) Stream(ctx context.Context, history []bungkus.Message) iter.Seq2[*Event, error] {
	return func(yield func(*Event, error) bool) {
		emit := func(e *Event) bool { return yield(e, nil) }
		forward := func(c *bungkus.ChatResponse) bool {
			if c.Reasoning != "" && !emit(&Event{Reasoning: c.Reasoning}) {
				return false
			}
			return c.Content == "" || emit(&Event{Text: c.Content})
		}
		chat := func(ctx context.Context, req *bungkus.ChatRequest) (*bungkus.ChatResponse, error) {
			return bungkus.Collect(func(yieldChunk func(*bungkus.ChatResponse, error) bool) {
				for chunk, err := range a.Provider.ChatStream(ctx, req) {
					if err == nil && !forward(chunk) {
						yieldChunk(nil, errStopped)
						return
					}
					if !yieldChunk(chunk, err) {
						return
					}
				}
			})
		}
		res, err := a.loop(ctx, history, chat, emit)
		switch {
		case errors.Is(err, errStopped):
		case err != nil:
			yield(nil, err)
		default:
			yield(&Event{Result: res}, nil)
		}
	}
}

// errStopped ends the loop when the Stream caller stops ranging.
var errStopped = errors.New("agent: stream stopped")

func (a *ReAct) loop(ctx context.Context, history []bungkus.Message,
	chat func(context.Context, *bungkus.ChatRequest) (*bungkus.ChatResponse, error), emit func(*Event) bool) (res *Result, err error) {
	name := a.Name
	if name == "" {
		name = "react"
	}
	ctx, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentAgent, Name: name}, history)
	defer func() {
		if errors.Is(err, errStopped) {
			end(res, nil) // the caller chose to stop; not a failure
		} else {
			end(res, err)
		}
	}()

	node := tool.NewNode(a.Tools...)
	req := a.Request
	req.Tools = slices.Concat(a.Request.Tools, node.Infos())
	maxSteps := a.MaxSteps
	if maxSteps == 0 {
		maxSteps = 10
	}
	res = &Result{}
	for range maxSteps {
		req.Messages = slices.Concat(a.Request.Messages, history, res.Messages)
		resp, err := chat(ctx, &req)
		if err != nil {
			return res, err
		}
		res.Usage.Add(resp.Usage)
		res.Messages = append(res.Messages, resp.Message)
		if !emit(&Event{Message: &resp.Message}) {
			return res, errStopped
		}
		if len(resp.ToolCalls) == 0 {
			return res, nil
		}
		results, err := node.Run(ctx, resp.Message)
		if err != nil {
			return res, err
		}
		for i := range results {
			res.Messages = append(res.Messages, results[i])
			if !emit(&Event{Message: &results[i]}) {
				return res, errStopped
			}
		}
	}
	return res, ErrMaxSteps
}
