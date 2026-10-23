// Package agent runs models in a loop with tools.
package agent

import (
	"context"
	"errors"
	"slices"

	"github.com/jerichosiahaya/bungkus"
	"github.com/jerichosiahaya/bungkus/tool"
)

// ErrMaxSteps is returned when the model still wants tools after MaxSteps calls.
var ErrMaxSteps = errors.New("agent: reached MaxSteps without a final answer")

// ReAct calls the model, runs the tools it asks for, feeds the results back,
// and repeats until the model answers without calling a tool.
type ReAct struct {
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

// Run continues the conversation in history until the model gives a final
// answer. On error, the Result holds the turns completed so far.
func (a *ReAct) Run(ctx context.Context, history []bungkus.Message) (*Result, error) {
	node := tool.NewNode(a.Tools...)
	req := a.Request
	req.Tools = slices.Concat(a.Request.Tools, node.Infos())
	maxSteps := a.MaxSteps
	if maxSteps == 0 {
		maxSteps = 10
	}
	res := &Result{}
	for range maxSteps {
		req.Messages = slices.Concat(a.Request.Messages, history, res.Messages)
		resp, err := a.Provider.Chat(ctx, &req)
		if err != nil {
			return res, err
		}
		res.Usage.Add(resp.Usage)
		res.Messages = append(res.Messages, resp.Message)
		if len(resp.ToolCalls) == 0 {
			return res, nil
		}
		results, err := node.Run(ctx, resp.Message)
		if err != nil {
			return res, err
		}
		res.Messages = append(res.Messages, results...)
	}
	return res, ErrMaxSteps
}
