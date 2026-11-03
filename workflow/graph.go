// Package workflow provides a small, synchronous graph runner for composing
// deterministic Go functions and bungkus components into bounded workflows.
// It does not persist execution state or run nodes concurrently.
//
// The package is experimental: its exported API may change before v1.0.0.
package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/jerichosiahaya/bungkus"
)

// ErrMaxExecutions indicates that the graph exceeded its node execution limit.
var ErrMaxExecutions = errors.New("workflow: reached maximum node executions")

// ErrNoRoute indicates a node returned an empty next-node name instead of
// terminating the workflow.
var ErrNoRoute = errors.New("workflow: node returned an empty route")

const defaultMaxExecutions = 100

// Node processes the workflow state and returns the next node's name. Return
// an empty next name and nil error to finish the workflow. State is updated in
// place; mutations made before an error remain visible in Result.State.
type Node[S any] func(context.Context, *S) (next string, err error)

// Graph is a named directed graph. Nodes choose their next node dynamically,
// which supports branches and loops without a separate routing language.
type Graph[S any] struct {
	name          string
	start         string
	maxExecutions int
	nodes         map[string]Node[S]
}

// Result describes a completed or partially completed graph run.
type Result[S any] struct {
	State      S
	Trace      []string // node names in execution order
	Executions int
}

// New creates an empty workflow graph. The name is used in observability
// callbacks; an empty name is reported as "graph".
func New[S any](name string) *Graph[S] {
	return &Graph[S]{name: name, maxExecutions: defaultMaxExecutions, nodes: make(map[string]Node[S])}
}

// Add registers a node. It returns an error for empty names, nil functions, or
// duplicate names. Nodes must be added before Run is called.
func (g *Graph[S]) Add(name string, node Node[S]) error {
	if name == "" {
		return errors.New("workflow: node name cannot be empty")
	}
	if node == nil {
		return fmt.Errorf("workflow: node %q is nil", name)
	}
	if _, exists := g.nodes[name]; exists {
		return fmt.Errorf("workflow: node %q already exists", name)
	}
	g.nodes[name] = node
	return nil
}

// StartAt selects the first node to execute. The name is validated when Run
// starts so graph construction can be performed in any order.
func (g *Graph[S]) StartAt(name string) { g.start = name }

// SetMaxExecutions sets the maximum total number of node calls in one Run.
// Values less than one are rejected. The default is 100.
func (g *Graph[S]) SetMaxExecutions(max int) error {
	if max < 1 {
		return errors.New("workflow: maximum executions must be at least 1")
	}
	g.maxExecutions = max
	return nil
}

// Run executes nodes serially until one returns an empty route or an error.
// Cancellation is checked before every node. On error, Result contains the
// caller's possibly partially mutated State and the execution trace so far.
func (g *Graph[S]) Run(ctx context.Context, initial S) (result *Result[S], err error) {
	result = &Result[S]{State: initial}
	name := g.name
	if name == "" {
		name = "graph"
	}
	ctx, end := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentWorkflow, Name: name}, nil)
	defer func() { end(result, err) }()

	if g.start == "" {
		return result, errors.New("workflow: start node is not set")
	}
	if _, ok := g.nodes[g.start]; !ok {
		return result, fmt.Errorf("workflow: start node %q does not exist", g.start)
	}

	current := g.start
	for result.Executions < g.maxExecutions {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		node, ok := g.nodes[current]
		if !ok {
			return result, fmt.Errorf("workflow: node %q routed to unknown node", current)
		}
		nodeCtx, nodeEnd := bungkus.StartRun(ctx, bungkus.RunInfo{Component: bungkus.ComponentWorkflowNode, Name: current}, nil)
		next, nodeErr := node(nodeCtx, &result.State)
		nodeEnd(nil, nodeErr)
		result.Executions++
		result.Trace = append(result.Trace, current)
		if nodeErr != nil {
			return result, fmt.Errorf("workflow: node %q: %w", current, nodeErr)
		}
		if next == "" {
			return result, nil
		}
		if _, ok := g.nodes[next]; !ok {
			return result, fmt.Errorf("workflow: node %q routed to unknown node %q", current, next)
		}
		current = next
	}
	return result, fmt.Errorf("workflow: executed %d nodes: %w", result.Executions, ErrMaxExecutions)
}
