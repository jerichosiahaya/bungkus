# Roadmap

This is a list of possible next steps, not a promise or a schedule. Priorities may change based on user feedback and maintainer capacity. Contributions are welcome; please open an issue to discuss larger changes before starting implementation.

## Near term

- [ ] **Verify supported providers against live APIs.** Run the opt-in live suite for Anthropic, Azure OpenAI, and BytePlus, then record provider/model/date and results in the README. Keep live tests opt-in; they can incur charges and depend on provider account access.
- [ ] **Add live coverage for embeddings and decisions.** Exercise the supported OpenAI-style embedding endpoints (OpenAI, Azure, BytePlus), OpenAI `/decisions`, and System One-compatible endpoints when credentials and suitable models are available. Keep credentials out of CI and repository history.
- [ ] **Verify provider-specific tool/reasoning behavior.** In particular, test Claude thinking blocks and DeepSeek `reasoning_content` through tool-use round trips against real APIs.
- [ ] **Publish compatibility guidance.** Document provider and model limitations found during live testing, with reproducible model/config details where possible.

## Explore, based on user demand

- [ ] **Vector-store integration examples.** Demonstrate implementing `rag.Retriever` with an external vector store, without adding a mandatory database dependency to the core package.
- [x] **Experimental graph orchestration MVP.** Added the bounded, synchronous `workflow` package for named nodes, branching, loops, typed state, and handler observability. See [GRAPH_ORCHESTRATION_PLAN.md](GRAPH_ORCHESTRATION_PLAN.md); the API is experimental and feedback is welcome.
- [ ] **Multi-agent patterns.** Gather use cases and design a composable approach before introducing orchestration abstractions.

## Out of scope / not applicable

- Anthropic and DeepSeek embeddings are not planned unless those providers expose embedding APIs; users can supply another `bungkus.Embedder`.
- Claude's deprecated `budget_tokens` thinking option is not planned; use `ReasoningEffort` instead.

## Scope and compatibility principles

- Keep the core package small and avoid mandatory dependencies where a small interface or example is sufficient.
- Prefer provider-specific features behind the existing common interfaces where feasible, and document differences that cannot be normalized safely.
- New public APIs should include tests, examples or documentation, and clear compatibility notes.
- Live API tests remain opt-in and must not make normal tests or CI depend on credentials or billable requests.
