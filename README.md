# Contract Tracer

Contract Tracer is a standalone Go library and command-line tool for discovering a bounded, evidence-backed investigation scope from source symbols or locations and an invariant hypothesis. It reports modeled calls, references, value flow, storage, event and lifecycle relationships together with candidate alternatives, coverage limits and unresolved boundaries. It does not prove a runtime invariant or guarantee complete caller recall.

## Requirements

- Go 1.27.0 or newer, as selected by the module's `go` directive.
- Dependencies declared in `go.mod`; Go package loading may resolve target-module dependencies using the normal Go toolchain.

The target is analyzed, not executed as an application.

## Install

Install the command-line tool with Go 1.27 or newer:

```sh
go install github.com/dominicnunez/contract-tracer/cmd/contract-trace@latest
```

The library module is `github.com/dominicnunez/contract-tracer` and its Go package name is `contracttrace`.

## Build and test

```sh
go test ./...
go build -trimpath -buildvcs=false -o contract-trace ./cmd/contract-trace
```

On Windows, use `-o contract-trace.exe`. For byte-identical builds in the same environment, use the same Go toolchain and build flags. This command was verified with native Go 1.27.1 from two independent fresh source copies using shared Go caches; it is not a cold-cache, cross-toolchain or clean-machine reproducibility guarantee.

## Run an investigation

```sh
./contract-trace \
  -root ./testdata/sample \
  -seed Validate \
  -invariant 'all accepted values are validated' \
  -depth 3 -max-nodes 100 \
  -output scope.json
```

On Windows, run `.\contract-trace.exe` from PowerShell. Seeds may be function names, method names, fully qualified function IDs, or discovered resource IDs. `-location file.go:line` seeds an indexed function, global or field declaration. Multiple seeds or locations are comma-separated. Use `-focus` to name symbols or resources that belong to the hypothesis; focus explains graph paths and does not remove structural scope.

Important options include `-tests`, `-tags`, `-expand-callbacks`, `-timeout`, `-format json|markdown`, `-save-analysis file.json.gz`, and `-resume file.json.gz`. The default traversal limits are depth 3 and 250 nodes. Reports retain truncation, frontiers, candidate inventories and unresolved boundaries when analysis is bounded. Saved exploration reuses the discovered graph after checking source and build identities; it does not rerun analysis to recover omitted relationships.

## Configuration

`-config config.json` accepts one strict JSON object. Unknown fields, invalid selectors and unsupported rule kinds are errors. Omitted fields keep defaults; explicitly setting an array to `[]` disables that default list. Symbols use `<Go import path>::<declared function or method>`.

Configured call rules describe typed SQL or event API signatures. Argument indexes exclude a method receiver; `handler_argument` is valid for `event_subscribe`.

```json
{
  "call_rules": [
    {"symbol":"example.com/service::Publish","kind":"event_publish","argument":1,"namespace":"orders"},
    {"symbol":"example.com/service::Subscribe","kind":"event_subscribe","argument":0,"handler_argument":1,"namespace":"orders"},
    {"symbol":"example.com/store::Query","kind":"sql_query","argument":0,"namespace":"orders"}
  ],
  "storage_scopes": [
    {"namespace":"orders","go_files":["internal/orders/**/*.go"],"sql_files":["migrations/orders/**/*.sql"]}
  ],
  "lifecycle_rules": [
    {"symbol":"example.com/service::Acquire","role":"acquire","namespace":"worker","identity":"origin","result":0},
    {"symbol":"example.com/service::Release","role":"release","namespace":"worker","identity":"origin","argument":0}
  ]
}
```

Lifecycle rules map one typed selector (`argument`, `result`, or `receiver`) to a role and namespace. `identity` is `origin` for modeled handle candidates or `value` for string keys. These mappings describe configured structural API roles; they do not prove success, ownership or ordering. `profiles/go-core.json` is an example that disables default event-field, lifecycle-name and SQL-method heuristics.

## Output and limits

JSON reports include nodes, source evidence, relationships, coverage, boundaries and `contract_complete: false`. Markdown is a summary. Saved analysis can be written as JSON or gzip JSON using a `.gz` suffix; resume validates the saved graph and source/build inputs before exploring it with new seeds, locations, focus or budgets.

The analyzer uses typed Go syntax/SSA and bounded value-flow models. Interface and callback targets may be possible candidates. Reflection, unsafe/cgo, unavailable dependency bodies and unselected build configurations are not fully modeled. Candidate flow is context-insensitive and bounded; it does not establish runtime object identity, invocation pairing, path feasibility, SQL transaction success, event delivery, cleanup success, goroutine scheduling or eventual termination. SQLite storage analysis is built in; other storage systems require configured adapters and their semantics remain unresolved.

## Contributing

Install Go and Python 3.12 or newer, then install the pinned development hook runner and enable the commit and push checks:

```sh
python -m pip install -r requirements-dev.txt
python tools/install_hooks.py
```

The installer keeps an existing pre-commit-managed commit hook, installs the standard commit checks, and installs a raw Git pre-push hook. It refuses to replace unrecognized hooks; move or explicitly chain a custom hook before installing these checks. Run the installer again after changing the Python interpreter used by the repository.

Commit checks validate YAML/JSON, whitespace, merge markers and private-key markers, lint GitHub Actions workflows, and verify that staged production Go files already pass `gofmt`. Before running push checks, the raw hook examines every Git ref update. Every non-deletion update must resolve to the checked-out `HEAD` commit, and the worktree must be clean. This permits annotated or lightweight tags that resolve to `HEAD`; tags pointing elsewhere and refs that do not resolve to a commit are rejected. Deletion-only pushes skip validation. Once all updates pass these checks, the hook runs `go vet ./...`, `go test ./... -count=1`, and `go build ./cmd/contract-trace` once. CI runs the Go checks on Linux and Windows with Go 1.27, and runs the repository-wide pre-commit checks and hook-helper tests.

Use [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/) for commit messages and squash titles, such as `fix(storage): preserve rollback edges`. Mark a breaking change with `!` after the type or scope (for example, `feat(api)!: change the config format`) or a `BREAKING CHANGE:` footer.
