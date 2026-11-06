# doctest

Keeps the Go examples in the repository's READMEs compiling.

Documentation drifts silently. An example that stopped matching the API still
renders fine on GitHub, so the reader copies it, the build fails, and they
reasonably conclude the library is broken. That is exactly what happened to
`pkg/client/README.md` before this package existed: it documented
`client.SearchParams`, `client.SetTimeout` and `client.LinkParams`, none of
which have ever existed.

## How it works

`TestReadmeExamplesCompile` walks every `README.md`, extracts the fenced Go
blocks, and compiles the ones marked as examples in a throwaway module that
`replace`s this repository, so the imports resolve to the working tree.

`TestReadmeSymbolsExist` parses every block (including fragments) and checks
that each `client.X` and `engine.X` it mentions is declared in that package.

## Marking a block

```markdown
```go example
package main
...
```
```

Two fence forms are recognised:

| Fence | Treatment |
|---|---|
| ` ```go ` | Fragment. Only symbol references are checked. |
| ` ```go example ` | Standalone program. Must start with `package main` and must compile. |

Use `go example` for the block a reader is meant to copy and run. Most blocks
in `pkg/client/README.md` are fragments illustrating one call, and stay as
plain `go`.

## Adding an example

1. Write the block in the README with the `go example` fence.
2. Run `go test ./internal/doctest/`.
3. Fix the example until it passes, not the test.

The test compiles but does not execute the code: a README example talks to a
server on `localhost:9091`, and requiring one in CI would turn a documentation
check into an integration suite.
