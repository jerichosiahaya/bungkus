# Contributing

Thanks for your interest in contributing to bungkus! Bug reports, documentation improvements, tests, and focused feature proposals are welcome.

## Before you start

- Search existing issues and pull requests to avoid duplicating work.
- For substantial changes or public API changes, open an issue first to discuss the design.
- Keep pull requests focused and update the README or examples when behavior changes.

## Development setup

- Install Go 1.25 or newer.
- Fork and clone the repository, then create a topic branch.
- The core test suite uses local fake servers and does not need provider API keys.

Run the checks before submitting:

```sh
gofmt -w $(find . -name '*.go' -not -path './.git/*')
go vet ./...
go test -race -cover ./...
```

Live API tests are optional, can incur provider charges, and require credentials. See the README's Development section; never include API keys in issues, commits, or logs.

## Pull requests

- Describe the problem and the approach taken.
- Include or update tests for behavior changes and bug fixes.
- Mention any breaking API changes and provider-specific limitations.
- Confirm formatting, vet, and tests pass locally.

By contributing, you agree that your contributions are provided under the repository's MIT license.
