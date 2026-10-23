// Package tool turns Go functions into tools a model can call, and runs the
// tool calls a model makes.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/jerichosiahaya/bungkus"
)

// Invokable is a tool that can be described to a model and run.
type Invokable interface {
	Info() bungkus.Tool
	// Invoke runs the tool with the model's JSON arguments and returns the
	// result text sent back to the model.
	Invoke(ctx context.Context, arguments string) (string, error)
}

// ErrInvalidArguments marks an error caused by the model sending bad arguments.
// Node reports such errors to the model instead of failing, so it can retry.
// Wrap it in custom Invokable implementations: fmt.Errorf("%w: ...", ErrInvalidArguments).
var ErrInvalidArguments = errors.New("invalid tool arguments")

// New makes a tool from a Go function. The JSON Schema for the arguments is
// derived from In, which must be a struct (or pointer to one):
//
//	type WeatherArgs struct {
//		City string `json:"city" desc:"City name"`
//		Unit string `json:"unit,omitempty" enum:"celsius,fahrenheit"`
//	}
//
// Field names follow the json tag. Fields are required unless they are
// pointers or tagged omitempty. desc sets a description; enum lists the
// allowed string values. If Out is a string it is returned to the model as is,
// otherwise it is sent as JSON.
//
// New panics if In is not a struct, since that is a programming error.
func New[In, Out any](name, description string, fn func(context.Context, In) (Out, error)) Invokable {
	t := reflect.TypeFor[In]()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("tool.New(%q): arguments type %s must be a struct", name, t))
	}
	params, _ := json.Marshal(schemaOf(t, map[reflect.Type]bool{})) // only strings, maps and slices: cannot fail
	return &funcTool[In, Out]{info: bungkus.Tool{Name: name, Description: description, Parameters: params}, fn: fn}
}

type funcTool[In, Out any] struct {
	info bungkus.Tool
	fn   func(context.Context, In) (Out, error)
}

func (t *funcTool[In, Out]) Info() bungkus.Tool { return t.info }

func (t *funcTool[In, Out]) Invoke(ctx context.Context, arguments string) (string, error) {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	var in In
	if err := json.Unmarshal([]byte(arguments), &in); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidArguments, err)
	}
	out, err := t.fn(ctx, in)
	if err != nil {
		return "", err
	}
	if s, ok := any(out).(string); ok {
		return s, nil
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// Node runs the tool calls in an assistant message, like Eino's ToolsNode.
type Node struct {
	tools map[string]Invokable
	infos []bungkus.Tool
}

func NewNode(tools ...Invokable) *Node {
	n := &Node{tools: make(map[string]Invokable, len(tools))}
	for _, t := range tools {
		info := t.Info()
		n.tools[info.Name] = t
		n.infos = append(n.infos, info)
	}
	return n
}

// Infos returns the tool definitions, for ChatRequest.Tools.
func (n *Node) Infos() []bungkus.Tool { return n.infos }

// Run executes msg.ToolCalls in parallel and returns one "tool" message per
// call, in call order. Unknown tools and ErrInvalidArguments errors become
// error text for the model; any other tool error cancels the rest and is returned.
func (n *Node) Run(ctx context.Context, msg bungkus.Message) ([]bungkus.Message, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	out := make([]bungkus.Message, len(msg.ToolCalls))
	var wg sync.WaitGroup
	for i, call := range msg.ToolCalls {
		wg.Go(func() {
			content, err := n.invoke(ctx, call)
			if err != nil {
				cancel(fmt.Errorf("tool %s: %w", call.Name, err))
				return
			}
			out[i] = bungkus.Message{Role: "tool", ToolCallID: call.ID, Content: content}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	return out, nil
}

func (n *Node) invoke(ctx context.Context, call bungkus.ToolCall) (string, error) {
	ctx, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentTool, Name: call.Name}, call)
	content, err := n.invokeTool(ctx, call)
	end(content, err)
	return content, err
}

func (n *Node) invokeTool(ctx context.Context, call bungkus.ToolCall) (string, error) {
	t, ok := n.tools[call.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", call.Name), nil
	}
	content, err := t.Invoke(ctx, call.Arguments)
	if errors.Is(err, ErrInvalidArguments) {
		return "error: " + err.Error(), nil
	}
	return content, err
}
