# Syncopa Core

**Syncopa Core** provides the open-source building blocks for high‑performance data scanning and movement.  
It exposes reusable Go packages and small CLI primitives that **syncopa-enterprise** builds on.

## Install
```bash
go get github.com/syncopasoft/syncopa-core@latest
```

## Quick Example
```go
import "github.com/syncopasoft/syncopa-core/scanner"

it := scanner.New(scanner.Config{Roots: []string{"/data"}})
for it.Next() {
    e := it.Entry()
    _ = e // use path, size, hashes, etc.
}
if err := it.Err(); err != nil { panic(err) }
```

## Contributing
PRs welcome. Please run `go fmt` and include tests where possible.

## Versioning
SemVer with release tags.

## License
Apache-2.0. See [LICENSE](./LICENSE). See [NOTICE](./NOTICE).
