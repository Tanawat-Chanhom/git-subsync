# git-subsync

A Git extension that will keep a folder in a Git repo in sync with a Google Drive folder, similar to a submodule.

**Status: v0.1.** This release only covers signing in to Google Drive. Syncing comes in a later release.

## Install

### macOS (Homebrew)

```sh
brew install --cask tanawat-chanhom/tap/tae-subsync
```

This puts `git-subsync` on your `PATH`, and git then runs it as `git subsync`.

The binary isn't signed or notarized. The cask removes the `com.apple.quarantine` attribute during install so Gatekeeper doesn't block it.

### Linux

Download the tarball for your architecture from [Releases](https://github.com/Tanawat-Chanhom/git-subsync/releases), then:

```sh
tar -xzf git-subsync_*_linux_amd64.tar.gz git-subsync
sudo install git-subsync /usr/local/bin/
```

## Usage

```sh
git subsync login               # sign in with Google in your browser
git subsync login --no-browser  # print the sign-in URL instead of opening a browser
git subsync whoami              # show the signed-in Google account
git subsync logout              # revoke the token and delete it locally
git subsync version             # version, commit and build date (also --version)
```

For help, use `git subsync -h`, or `git subsync <command> --help`. Plain `git subsync --help` won't work: git rewrites it to `git help subsync`, which looks for a man page that doesn't exist.

### Signing in

`login` starts a temporary server on `127.0.0.1`, opens Google's consent page, and waits up to 3 minutes for the redirect back. The browser has to run on the same machine, because that's where Google redirects. When it finishes, it prints `Logged in as <email>`.

git-subsync asks for full Google Drive access (`https://www.googleapis.com/auth/drive`). Later versions need it to open existing Drive folders by URL.

### Where the token is stored

| OS    | Path |
|-------|------|
| macOS | `~/Library/Application Support/git-subsync/token.json` |
| Linux | `$XDG_CONFIG_HOME/git-subsync/token.json` (default `~/.config/git-subsync/token.json`) |

Set `GIT_SUBSYNC_CONFIG_DIR` to use a different directory. The directory is created with mode `0700` and the file with `0600`. Access tokens refresh automatically, and the new token is saved back to the file.

While the Google OAuth app is in **Testing** mode, Google expires refresh tokens after 7 days. When that happens, commands print `Session expired. Run git subsync login again.`

## Uninstall

```sh
git subsync logout                    # optional: revoke access with Google first
brew uninstall --cask tae-subsync     # add --zap to also delete the saved token
```

On Linux, delete the binary and `~/.config/git-subsync`.

You can also revoke access at any time from your Google Account's [third-party connections](https://myaccount.google.com/connections) page.

## Development

You need the Go version listed in `go.mod`, and a Google OAuth client of type **Desktop app** with the Drive API enabled. The client ID and secret come from environment variables, which override any client built into a release:

```sh
export GIT_SUBSYNC_CLIENT_ID=...
export GIT_SUBSYNC_CLIENT_SECRET=...
go build -o "$(go env GOPATH)/bin/git-subsync" ./cmd/git-subsync
git subsync login
```

Never commit the client secret. Downloaded `client_secret_*.json` files are gitignored.

```sh
go vet ./...
go test -race ./...
GIT_SUBSYNC_CONFIG_DIR="$(mktemp -d)" git subsync login   # use a throwaway token dir
```

| Package | Purpose |
|---------|---------|
| `cmd/git-subsync` | entry point |
| `internal/cli` | cobra commands |
| `internal/auth` | OAuth flow, token store, client config (no CLI dependencies) |
| `internal/gdrive` | Drive client wrapper (no CLI dependencies) |
| `internal/version` | build metadata injected via `-ldflags` |

### Releasing

Pushing a `vX.Y.Z` tag runs [`.github/workflows/release.yml`](.github/workflows/release.yml), which uses GoReleaser to:

- build darwin/linux × amd64/arm64,
- create a GitHub Release,
- push the `tae-subsync` cask to [`Tanawat-Chanhom/homebrew-tap`](https://github.com/Tanawat-Chanhom/homebrew-tap).

It needs these Actions secrets:

| Secret | Value |
|--------|-------|
| `GOOGLE_CLIENT_ID` | OAuth client ID, built into the binary |
| `GOOGLE_CLIENT_SECRET` | OAuth client secret, built into the binary |
| `HOMEBREW_TAP_GITHUB_TOKEN` | fine-grained PAT with *Contents: read & write* on `homebrew-tap` only |

Before tagging:

```sh
goreleaser check
goreleaser release --snapshot --clean
git tag v0.1.0 && git push origin v0.1.0
```

## License

[MIT](LICENSE)
