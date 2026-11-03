# Changelog

Notable changes to bungkus are documented here.

## [v0.1.0-alpha.1] - 2026-11-03

Initial public alpha release.

- Unified Go client for OpenAI, Anthropic, Azure OpenAI, BytePlus ModelArk, and DeepSeek chat APIs.
- Chat completions, streaming, tool calling, request retries, and provider-specific reasoning support.
- Typed function tools and a ReAct agent with streaming and parallel tool execution.
- Prompt templates, in-memory embedding retrieval, and decision-model APIs.
- Optional logging and OpenTelemetry tracing integrations.
- Tested with local fake API servers; only a subset of provider integrations has been validated against live APIs. See the README for current live-test results and limitations.

This is an early alpha. APIs and behavior may change between releases. Review the README and run your own provider-specific tests before using in production.
