# git-subsync

A Git extension (`git subsync`) that keeps a folder in a Git repo in sync with a Google Drive folder. Go CLI, released with GoReleaser and a Homebrew cask.

## Layout

- `cmd/git-subsync/` — `main`, wires up the CLI.
- `internal/cli/` — one file per command (`add`, `sync`, `list`, `remove`, `login`, `logout`, `whoami`, `version`), plus progress output.
- `internal/syncer/` — scans local and Drive state (`scan.go`), plans actions (`plan.go`) and runs them (`sync.go`). Most sync bugs live here.
- `internal/gdrive/` — Drive API client and folder-link parsing. `drivetest/` is an in-memory fake Drive used by tests.
- `internal/repo/` — finds the repo root, reads/writes `.gitsubsync` and `.gitignore`.
- `internal/state/` — per-folder sync state stored on disk.
- `internal/auth/` — Google OAuth login, token storage and revocation.
- `internal/version/` — build-time version info.

## Tests

- Run everything: `go test ./...` (CI also runs `go vet ./...` and `go test -race ./...` on Linux and macOS).
- Tests never touch the network or a real Google account. Use `internal/gdrive/drivetest` for Drive.
- Tests sit next to the code as `*_test.go`, mostly table-driven.

## Rules

- Never commit OAuth client secrets. `client_secret_*.json` and `.env*` are git-ignored; credentials reach release builds through GoReleaser ldflags only.
- Progress messages go to stderr; stdout carries results only.
- Every command works from any directory in the repo, and user-facing paths are relative to the repo root.
- Conflicts must never lose data (one side is kept as a `_copy_` file).
- Don't add dependencies without a reason.

## AI bug-fix pipeline

`ai-pipeline/` and the `ai-*.yml`, `deploy-staging.yml` workflows run an automated bug-fix flow into the `staging` branch. See `ai-pipeline/SETUP.md`. The AI fixer may not modify `.github/`, `ai-pipeline/`, `internal/auth/`, `go.mod`/`go.sum`, or this file.
