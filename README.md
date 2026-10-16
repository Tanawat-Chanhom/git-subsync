# git-subsync

A Git extension that will keep a folder in a Git repo in sync with a Google Drive folder, similar to a submodule.

**Status: v0.1.** This release covers signing in to Google Drive and installing with Homebrew. Syncing comes in a later release.

## What's in v0.1

- `git subsync login`, `logout`, `whoami` and `version` commands.
- Browser sign-in with Google using OAuth with PKCE. The token is stored locally and refreshes automatically.
- `whoami` makes a real Drive API call (`about.get`), so it proves Drive access works.
- Release builds for macOS and Linux (amd64 and arm64), published as a Homebrew cask (`git-subsync`).

Not built yet: `add`, `sync`, a `.gitsubsync` config file, any upload or download, Windows support, and a homebrew-core formula.

## Install

### macOS (Homebrew)

```sh
brew install --cask tanawat-chanhom/tap/git-subsync
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

| Command | What it does |
|---------|--------------|
| `login` | Signs in, saves the token, and prints `Logged in as <email>`. |
| `whoami` | Prints `Name <email>`. Exits 1 with a clear message if you're not logged in or the session has expired. |
| `logout` | Asks Google to revoke the token, then deletes it locally. If Google can't be reached, it prints a warning and still deletes the local token. |
| `version` | Prints `git-subsync <version> (commit <sha>, built <date>)`. |

Progress messages go to stderr, so stdout only carries results.

### Signing in

1. `login` starts a temporary server on `127.0.0.1` on a random free port.
2. It opens Google's consent page in your browser and also prints the URL in case the browser doesn't open.
3. Google redirects back to `http://127.0.0.1:<port>/callback`. git-subsync checks the random `state` value and accepts only the first callback.
4. It exchanges the code for a token using the PKCE (S256) verifier.

The flow times out after 3 minutes. The browser has to run on the same machine, because that's where Google redirects.

git-subsync requests offline access with `prompt=consent`, so Google always returns a refresh token. It asks for full Google Drive access (`https://www.googleapis.com/auth/drive`), because later versions need to open existing Drive folders by URL, and the narrower `drive.file` scope can't do that.

### Where the token is stored

| OS    | Path |
|-------|------|
| macOS | `~/Library/Application Support/git-subsync/token.json` |
| Linux | `$XDG_CONFIG_HOME/git-subsync/token.json` (default `~/.config/git-subsync/token.json`) |

Set `GIT_SUBSYNC_CONFIG_DIR` to use a different directory. The directory is created with mode `0700` and the file with `0600`. Writes are atomic: the token is written to a temporary file, which is then renamed. Access tokens refresh automatically, and the new token is saved back to the file, so later runs reuse it.

While the Google OAuth app is in **Testing** mode, Google expires refresh tokens after 7 days. When that happens, commands print `Session expired. Run git subsync login again.`

## Uninstall

```sh
git subsync logout                    # optional: revoke access with Google first
brew uninstall --cask git-subsync     # add --zap to also delete the saved token
```

On Linux, delete the binary and `~/.config/git-subsync`.

You can also revoke access at any time from your Google Account's [third-party connections](https://myaccount.google.com/connections) page.

## Troubleshooting

| Symptom | Cause / fix |
|---------|-------------|
| `Not logged in. Run git subsync login.` | There's no saved token. Run `git subsync login`. |
| `Session expired. Run git subsync login again.` | Google rejected the refresh token (`invalid_grant`): it was revoked, or the 7-day expiry for Testing-mode apps applied. Log in again. |
| Google shows "Access blocked" or "app has not completed verification" | Your Google account isn't on the OAuth app's test-user list. Ask the maintainer to add you. |
| The browser doesn't open | Copy the printed URL into a browser on the same machine, or use `--no-browser`. |
| `no OAuth client configured` | This build has no client built in (e.g. a local `go build`). Set `GIT_SUBSYNC_CLIENT_ID` and `GIT_SUBSYNC_CLIENT_SECRET`. |
| `git subsync --help` says there's no manual entry | Use `git subsync -h` (see [Usage](#usage)). |

## Development

You need the Go version listed in `go.mod`, and a Google OAuth client of type **Desktop app** with the Drive API enabled (see [Maintainer setup](#maintainer-setup-one-time)).

```sh
export GIT_SUBSYNC_CLIENT_ID=...
export GIT_SUBSYNC_CLIENT_SECRET=...
go build -o "$(go env GOPATH)/bin/git-subsync" ./cmd/git-subsync
GIT_SUBSYNC_CONFIG_DIR="$(mktemp -d)" git subsync login   # use a throwaway token dir
```

**Where the OAuth client comes from:**
1. If either environment variable is set, both are required, and they're used together.
2. Otherwise, git-subsync uses the client built into the binary at release time (`-ldflags -X`).
3. If neither is available, it exits with `no OAuth client configured`.

A client ID from one source is never paired with a secret from the other.

Never commit the client secret. `.gitignore` excludes downloaded `client_secret_*.json` files and `.env` files.

```sh
go vet ./...
go test -race ./...
```

The tests run offline and don't need a Google account. They cover:
- the token store: permissions, atomic writes, missing or corrupt files
- client-config resolution
- callback `state` validation
- the full login flow against a fake browser and token server, checking PKCE, `access_type=offline` and `prompt=consent`
- refreshed tokens being written back to disk
- `invalid_grant` being detected through the Drive client's error wrapping
- token revocation
- the CLI error messages

The real Google sign-in is tested by hand.

| Package | Purpose |
|---------|---------|
| `cmd/git-subsync` | entry point (handles Ctrl-C) |
| `internal/cli` | cobra commands. Maps errors to messages like "Not logged in" and "Session expired". |
| `internal/auth` | OAuth flow, token store, client config, token revocation (no CLI dependencies) |
| `internal/gdrive` | Drive client wrapper (no CLI dependencies) |
| `internal/version` | build metadata injected via `-ldflags` |

`auth` and `gdrive` don't import cobra, so future `add` and `sync` commands can reuse them. `internal/cli/session.go` already builds an authenticated Drive client from the saved token.

## Maintainer setup (one-time)

### Google Cloud

1. Create a project and enable the **Google Drive API**.
2. Configure the OAuth consent screen as **External**, with publishing status **Testing**. Add every account that should be able to sign in as a test user (at most 100).
3. Create an OAuth client of type **Desktop app**. Save the client ID and secret, because the secret is only shown once.

### GitHub

1. Create the public repo `homebrew-tap` and give it at least one commit (e.g. "Add a README").
2. Create a fine-grained PAT with access to `homebrew-tap` only, with the permission **Contents: read & write**.
3. In `git-subsync`, go to **Settings → Secrets and variables → Actions → Secrets → Repository secrets** and add the three secrets below. They must be repository secrets. Don't add them in the *Variables* tab or as environment secrets. Otherwise the release workflow's "Check required secrets" step fails.

| Secret | Value |
|--------|-------|
| `GOOGLE_CLIENT_ID` | OAuth client ID, built into release binaries |
| `GOOGLE_CLIENT_SECRET` | OAuth client secret, built into release binaries |
| `HOMEBREW_TAP_GITHUB_TOKEN` | the PAT from step 2 |

## Releasing

**CI** ([`ci.yml`](.github/workflows/ci.yml)) runs on pushes to `main` and on pull requests: `go vet`, `go test -race` and `go build` on Ubuntu and macOS, plus `goreleaser check`.

**Release** ([`release.yml`](.github/workflows/release.yml)) runs when you push a `v*` tag:
1. It checks that all three secrets are set, and stops before building anything if one is missing.
2. GoReleaser builds darwin/linux × amd64/arm64 with the version, commit, date and OAuth client injected through ldflags.
3. GoReleaser creates a GitHub Release with the tarballs and `checksums.txt`.
4. GoReleaser pushes `Casks/git-subsync.rb` to [`Tanawat-Chanhom/homebrew-tap`](https://github.com/Tanawat-Chanhom/homebrew-tap). The tap's `cask_renames.json` maps the old `tae-subsync` name to it.

```sh
goreleaser check
goreleaser release --snapshot --clean      # dry run; writes the cask to dist/homebrew/Casks/
git tag -a vX.Y.Z -m "vX.Y.Z" && git push origin vX.Y.Z
```

After a release, check for:
- a GitHub Release with 4 tarballs and `checksums.txt`
- a commit `Update git-subsync cask to vX.Y.Z` in the tap
- a working `brew install --cask tanawat-chanhom/tap/git-subsync`

If the release fails at "Check required secrets", nothing has been published. Fix the secrets and click **Re-run all jobs**; the tag doesn't need to be recreated.

GoReleaser is pinned to v2.18.3 in both workflows. The cask uses `hooks.post.install`, which generates a `postflight` block. Homebrew 7 accepts it but warns that `postflight` is deprecated in favor of `postflight_steps`. Once GoReleaser is upgraded to v2.19 or later, switch to `install_steps`, because `goreleaser check` fails on the deprecated field. Then check that the Homebrew warning is gone.

### Security notes

- The OAuth client secret is never committed. Release builds embed it, so anyone with the binary can extract it. Google treats Desktop-app client secrets as non-confidential. The real protection is PKCE plus the loopback redirect.
- `drive` is a *restricted* scope. While the app is in Testing mode, only listed test users can sign in. Opening it to everyone requires Google's app verification plus a CASA security assessment.

## Changelog

### v0.1.0 (2026-10-16)

- First release: `login` (with `--no-browser`), `logout`, `whoami`, `version`.
- Loopback OAuth with PKCE and state validation, a 3-minute timeout, and offline access.
- Token store with `0700`/`0600` permissions; refreshed tokens are written back to disk.
- Session-expiry detection (`invalid_grant`) with an actionable message.
- GoReleaser and GitHub Actions release to GitHub Releases and a Homebrew cask. It was first published as `tae-subsync` and renamed to `git-subsync` the same day. The old name still resolves through the tap's `cask_renames.json`; Homebrew 7 asks you to run `brew trust --cask tanawat-chanhom/tap/git-subsync` first.

## License

[MIT](LICENSE)
