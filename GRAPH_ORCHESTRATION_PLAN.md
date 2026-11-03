# Graph orchestration plan

## Goal

Explore a small, provider-agnostic Go package for composing existing bungkus capabilities—especially `agent.ReAct`—into explicit workflows with sequential steps, branching, and bounded loops. Keep the feature optional, dependency-free, and separate from provider-specific request/response code.

**Status: experimental MVP implemented.** This plan records the rationale and remaining validation work; the public API is in `workflow/graph.go` and may change before v1.0.0. User feedback and real use cases are still needed before considering stabilization.

## Problem to solve

`agent.ReAct` handles a single model/tool loop well, but users may want a workflow such as:

1. classify an input;
2. route it to a specialist agent or deterministic function;
3. optionally retrieve context;
4. run a review or approval step;
5. return a final result, with explicit limits and observability.

Today users can write this control flow themselves. Graph orchestration is worthwhile only if a small reusable API improves visibility, testability, state handling, and failure behavior without becoming a general workflow engine.

## Proposed MVP

An experimental `workflow` (or `graph`) package that runs a directed graph of named nodes over caller-defined state:

- **State:** caller-owned typed Go value, passed as `any` or through a generic `Graph[S]` API. Prefer generics if they produce an ergonomic API without compromising nodes that need dynamic values.
- **Node:** a named function that receives `context.Context` and mutable/returned state, and returns the next node name or a terminal result plus an error.
- **Edges:** explicit transitions. Support a single next node and conditional routing implemented by the node; avoid a separate expression language.
- **Termination:** explicit finish state; reject invalid destinations and detect missing routes.
- **Execution limits:** configurable maximum node executions to prevent accidental infinite cycles; context cancellation respected at each step.
- **Errors:** return partial state and execution trace with a wrapped node error; do not silently retry.
- **Observability:** emit existing bungkus handler runs for workflow and node execution, with parent-child context propagation and no prompt/state content recorded unless callers opt into existing content instrumentation.
- **Composition:** adapters or examples show how to use `agent.ReAct`, `rag.Retriever`, `Decider`, and ordinary Go functions as nodes. Do not couple the workflow package to provider implementations.
- **Tests:** fully deterministic fake nodes for sequential, branch, loop, cancellation, invalid route, node error, max-execution, and handler nesting behavior. No API keys or network required.

### Illustrative API only (not finalized)

```go
g := workflow.New[TicketState]()
g.Add("classify", classify)
g.Add("retrieve", retrieve)
g.Add("answer", answer)
g.Add("refund", refund)
g.StartAt("classify")
g.SetMaxExecutions(20)

result, err := g.Run(ctx, TicketState{Input: text})
```

A node can select its next destination based on updated state, e.g. route from `classify` to `refund` or `retrieve`. The exact signature should be settled with example workflows before implementation.

## Explicit non-goals for the first version

- Durable/persisted execution, checkpointing, replay, or distributed scheduling.
- Parallel/fan-out execution, joins, concurrency control, or event queues.
- A visual editor, graph DSL/config format, or graph serialization.
- Built-in retries, compensation/saga semantics, human-in-the-loop waiting, or time scheduling.
- A multi-agent framework or agent-to-agent protocol.
- Replacing or expanding `agent.ReAct`; it remains the tool-use loop primitive.
- Vendor-specific orchestration or automatic model/tool selection.

These can be reconsidered only if concrete users and use cases justify their complexity.

## Design decisions to resolve before coding

1. **State API:** generic typed state versus `any`; if typed, should nodes mutate `*S` or return a new `S`? Favor clarity and predictable partial state on failure.
2. **Graph construction:** immutable builder versus mutable registration methods; reject duplicate names and invalid start nodes early where possible.
3. **Transition model:** node returns destination string, typed transition, or terminal sentinel; make accidental empty transitions impossible or clearly erroneous.
4. **Node identity and observability:** stable names, run metadata, and what data is safe to include in handler events.
5. **Experimental status:** whether to ship in an `internal` prototype first or expose an explicitly experimental public package with compatibility caveats.
6. **Streaming:** whether a later milestone needs workflow events. The MVP can be synchronous and expose node-level events only if there is a concrete consumer need.

## Milestones / acceptance criteria

### 0. Validate demand and API shape

- [ ] Collect at least two concrete workflow examples not adequately served by `agent.ReAct` plus ordinary Go control flow.
- [ ] Sketch the API for a classifier-to-specialist route and a retrieve-answer-review loop.
- [ ] Review public API complexity and agree on the non-goals before implementation.

### 1. Prototype and evaluate

- [ ] Build a small internal prototype with typed state, named nodes, conditional transitions, and max executions.
- [ ] Write table-driven tests for normal completion, branches, bounded loops, cancellation, invalid destinations, and errors.
- [ ] Compare code size/readability with direct Go composition; abandon or simplify if the abstraction is not clearly better.

### 2. Experimental MVP

- [x] Publish the package API and document its experimental status and compatibility expectations.
- [x] Add observability consistent with existing handlers and tracing; workflow state is not recorded by default.
- [ ] Add examples composing `agent.ReAct`, `Decider`, and RAG.
- [x] Add package docs and README link; keep core dependencies unchanged.
- [ ] Run formatting, `go vet`, race-enabled tests, and review behavior under cancellation and node errors.

### 3. Revisit based on usage

- [ ] Gather feedback from users applying it to real workflows.
- [ ] Consider streaming events, persistence, parallel branches, or checkpointing only when validated use cases require them.

## Risks and guardrails

- A generic workflow engine can quickly outgrow bungkus's scope. Keep the initial package small and synchronous.
- Mutable state can create surprising behavior; choose one state update model and document partial results on error.
- Cycles can run forever; require a finite default execution cap and honor context cancellation.
- Hidden retries or concurrency make model calls costly and behavior harder to predict; keep both out of MVP.
- Prompts and workflow state may contain sensitive user data; do not record content by default.
- New exported API is difficult to retract; validate with examples and issue discussion before stabilizing it.
