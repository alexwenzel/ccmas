# ccmas
claude code - multi agent setup

## Voraussetzungen

- Go 1.27 (`go.mod`: `go 1.27.3`)
- Python ≥ 3.12 und [uv](https://docs.astral.sh/uv/) (getestet mit uv 0.13.7)
- make

## Prüfen und bauen

```bash
make check     # alles: test, race, go vet, build, py-test
make test      # go test ./...
make race      # go test -race ./...
go vet ./...
make build     # baut bin/ccmas
make py-test   # Python-Runner-Tests (uv run --frozen pytest)
```

Danach: `bin/ccmas --help` und `bin/ccmas version`. Die Python-Umgebung des Runners wird aus
`runners/claude_sdk/uv.lock` hergestellt (`--frozen`). Architektur, CLI-Gruppen, Exit-Codes und
Agenten-Einstieg: [`docs/architecture/ccmas-design.md`](docs/architecture/ccmas-design.md).
