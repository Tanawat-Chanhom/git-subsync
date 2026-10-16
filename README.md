# git-subsync

A Git extension that keeps a folder in a Git repo in sync with a Google Drive folder, similar to a submodule.

**Status: v0.2.** Link a folder to Google Drive with `add`, then sync it with `sync`, both ways or one way.

## What's in v0.2

- `git subsync add`, `sync`, `list` and `remove` commands.
- Three modes: `both` (two-way, the default), `download` (Drive → local only) and `upload` (local → Drive only).
- Conflicts never lose data: one version is kept as a `_copy_` file.
- A committed `.gitsubsync` file, so teammates get the same folders by running `git subsync sync`.
- Synced files are added to `.gitignore`, so Git never tracks them.
- Drive commands sign you in first if you aren't logged in.

v0.1 added `login`, `logout`, `whoami`, `version` and the Homebrew cask (macOS and Linux, amd64 and arm64).

Not built yet: syncing deletes, `status` and `config` commands, automatic sync (Git hooks or watching), exporting Google Docs, ignore patterns, parallel transfers, Windows support, and a homebrew-core formula.

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
git subsync add <drive-folder-url> <path>  # link <path> to a Drive folder and sync it
git subsync sync                           # sync every linked folder
git subsync sync <path>... --dry-run       # show what a sync would do
git subsync list                           # linked folders, modes and last sync times
git subsync remove <path>                  # unlink a folder (keeps local files, never touches Drive)

git subsync login                          # sign in with Google in your browser
git subsync login --no-browser             # print the sign-in URL instead of opening a browser
git subsync whoami                         # show the signed-in Google account
git subsync logout                         # revoke the token and delete it locally
git subsync version                        # version, commit and build date (also --version)
```

For help, use `git subsync -h`, or `git subsync <command> --help`. Plain `git subsync --help` won't work: git rewrites it to `git help subsync`, which looks for a man page that doesn't exist.

| Command | What it does |
|---------|--------------|
| `add` | Links a folder to a Drive folder and runs the first sync. See [Linking a folder](#linking-a-folder). |
| `sync` | Syncs the given folders, or all of them. See [Syncing](#syncing). |
| `list` | Prints each folder's path, mode, Drive folder name and last sync time (`never` if it hasn't synced). It works offline. |
| `remove` | Removes the folder from `.gitsubsync` and `.gitignore` and deletes its sync state. Local files stay unless you pass `--delete-files`. Drive is never changed. |
| `login` | Signs in, saves the token, and prints `Logged in as <email>`. |
| `whoami` | Prints `Name <email>`. Exits 1 with a clear message if you're not logged in or the session has expired. |
| `logout` | Asks Google to revoke the token, then deletes it locally. If Google can't be reached, it prints a warning and still deletes the local token. |
| `version` | Prints `git-subsync <version> (commit <sha>, built <date>)`. |

Progress messages go to stderr, so stdout only carries results. Every command works from any directory in the repo, and `<path>` is always relative to the repo root.

### Linking a folder

```sh
git subsync add https://drive.google.com/drive/folders/1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD design
git add .gitsubsync .gitignore
git commit -m "Add design folder from Google Drive"
```

`add` does the following:
1. It reads the folder ID from the link. It accepts `.../drive/folders/<id>`, `.../drive/u/<n>/folders/<id>`, links with a query string such as `?usp=sharing`, `.../open?id=<id>`, and a raw folder ID.
2. It checks `<path>`. The path can't be `.git`, already linked, inside or around another linked folder, or tracked by Git. A folder that already has files in it needs `--force`.
3. It checks that your account can open the Drive folder. If it can't, the error names the account.
4. It adds the folder to `.gitsubsync`, adds `/<path>/` to `.gitignore` under a `# git-subsync` comment, creates the folder, and runs the first sync.

| Flag | Meaning |
|------|---------|
| `--mode both\|download\|upload` | Sync direction, `both` by default. See [Modes](#modes). |
| `--no-sync` | Link the folder without syncing it. |
| `--force` | Allow a folder that already has files in it. Files that differ from Drive become conflicts, so nothing is lost. |

Commit `.gitsubsync` and `.gitignore`. A teammate who clones the repo runs `git subsync sync`, which creates the folder and downloads it.

### Syncing

`git subsync sync` syncs every linked folder, and `git subsync sync <path>...` syncs only those. `--dry-run` prints what would happen, including conflicts, without changing anything.

`sync` asks before the first sync of a folder that already has files in `both` or `upload` mode. "First" means the folder has never been synced on this computer, or its Drive folder ID in `.gitsubsync` has changed since. `.gitsubsync` is committed by whoever edits the repo, so without this check a pull could link a folder of yours to their Drive folder, and the next sync would upload your files. Without a terminal, `sync` refuses instead of asking. Folders created by `add`, or missing ones like in a fresh clone, aren't asked about.

```
📥 download  docs/brief.pdf
🔀 conflict  logo.png → logo_copy_20261016-161200.png (changed locally and on Drive; local version kept as the copy)
📥 download  old.txt (deleted locally; deletes aren't synced)
📤 upload    notes.md
⏩ skip      Budget — Google Sheets file
✅ 2 downloaded, 1 uploaded, 1 conflict, 1 skipped
```

| Icon | Meaning |
|------|---------|
| 📥 | downloaded from Drive |
| 📤 | uploaded to Drive |
| 🔀 | conflict, resolved by keeping both versions |
| 🛑 | conflict left alone (upload mode), or the summary of a run that left one |
| ⏩ | skipped |
| ❌ | failed, or the summary of a run where something failed |
| ✅ | the summary of a run where everything worked |
| 🔍 | scanning, or the summary of a dry run |

On a terminal, a status line under the output shows what's happening. During the scan, it shows how many files each side has. During transfers, it shows the file being downloaded or uploaded, a bar with the overall percentage, the bytes done and total, the speed, and how many files are done:

```
⠼ 📥 photos/big.mov      [██████████░░░░░░░░░░]  52%  48.1/92.0 MB    5.2 MB/s  3/7
```

The status line is drawn on stderr and cleared when the sync ends. It appears only when stderr is a terminal and `TERM` isn't `dumb`, so scripts and logs get only the result lines.

For every file, a sync compares three MD5 checksums: the local file, the Drive file, and the version from the last sync, which is kept in the local state. Downloads run before uploads.

| Case | `both` | `download` | `upload` |
|------|--------|------------|----------|
| New local file | upload | skip | upload |
| New Drive file | download | download | skip |
| On both sides, never synced, same content | record | record | record |
| On both sides, never synced, different content | conflict | conflict | conflict |
| Changed on Drive only | download | download | skip |
| Changed locally only | upload | skip | upload |
| Changed on both sides, differently | conflict | conflict | conflict |
| Changed on both sides, to the same content | record | record | record |
| Deleted locally | download again | download again | skip |
| Deleted on Drive | upload again | skip | upload again |
| Deleted on both sides | forgotten | forgotten | forgotten |

*record* means the sides already match, so only the state is updated. *skip* means the file is left alone and reported on every sync.

Deletes aren't synced yet. A file deleted on one side comes back from the other side, and renaming a file brings the old name back on the next sync. To get rid of a file, delete it locally and on Drive before you sync.

### Modes

The mode is stored in `.gitsubsync`, so everyone who syncs the repo uses the same one. To change it, run `git config -f .gitsubsync subsync.<path>.mode <mode>`.

| Mode | Direction |
|------|-----------|
| `both` | Two-way. This is the default. |
| `download` | Drive → local. It never writes to Drive. Local-only files and changes are left alone and reported. |
| `upload` | Local → Drive. It never writes local files. Drive-only files and changes are left alone and reported. |

### Conflicts

A conflict is a file that changed on both sides since the last sync, or that differs between the two sides on the first sync. One version is kept as `<name>_copy_<YYYYMMDD-HHMMSS><ext>`, using local time, e.g. `file1_copy_20261016-161200.txt`. If that name is taken, `-2`, `-3` and so on is added.

| Mode | What happens |
|------|--------------|
| `both` | The local file is renamed to the `_copy`, the Drive version is downloaded under the original name, and the copy is uploaded. Both sides end up with both files. |
| `download` | The local file is renamed to the `_copy`, which stays local, and the Drive version is downloaded. |
| `upload` | Nothing is uploaded for that file. The other files still sync, and the exit code is 2. |

In upload mode, the conflict shows who changed the file on Drive and when:

```
🛑 conflict  docs/file1.txt — changed locally and on Drive (by Somchai, 2026-10-16 15:02). Not uploaded.
🛑 3 uploaded, 1 conflict unresolved
💡 To upload your version, run: git subsync sync test-git-sync --force
   (the Drive version will be kept as file1_copy_<YYYYMMDD-HHMMSS>.txt)
```

There are two ways to resolve it:
- Run `sync --force`. It renames the Drive file to the `_copy` without downloading it, then uploads your file under the original name. The `_copy` keeps the old file's Drive ID, so its share links and comments stay with the `_copy`.
- Make your local file identical to the Drive version. The next sync then records the file as resolved.

`--force` only does something in upload mode. In the other modes, it prints a notice.

### What isn't synced

These are skipped, with a `skip` line on every sync:
- Google Docs, Sheets, Slides, shortcuts and other Google-native files, because Drive has no file to download for them.
- Symbolic links.
- Drive names that can't be local file names, e.g. names containing `/`.
- Two or more items with the same name in one Drive folder.
- Names that differ only in letter case or accents, such as `Report.pdf` and `report.pdf`. These are skipped on every OS, because macOS can't store both.
- A path that's a file on one side and a folder on the other.

`.DS_Store` and `Thumbs.db` are ignored without a message. Empty folders aren't synced. Folders are created on whichever side the mode can write to, as files need them.

### Safety

- Downloads are written to a temporary file in the same folder and checked against Drive's MD5 before being renamed into place.
- A changed file is uploaded as a new version of the same Drive file, so it keeps its file ID. Files over 8 MiB use resumable uploads. The MD5 that Drive reports is checked against the bytes sent.
- Right before overwriting a file on either side, git-subsync checks that it hasn't changed since the scan. If it has, the file is skipped until the next sync.
- The state is saved after every file, so you can rerun an interrupted sync.
- A lock file stops two syncs of the same folder from running at once.
- `.gitsubsync` paths must be inside the repo, and a path that goes through a symbolic link is refused, so a committed file can't make git-subsync write or delete outside the repo.
- Rate limits and server errors are retried with exponential backoff.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success. `--dry-run` always exits 0. |
| 1 | An error, or at least one file failed to sync. This takes precedence over 2. |
| 2 | Unresolved conflicts in upload mode. See [Conflicts](#conflicts). |

### Files git-subsync keeps

| File | Committed | Contents |
|------|-----------|----------|
| `.gitsubsync` | yes | One section per folder, in Git config format: path, Drive folder ID and mode |
| `.gitignore` | yes | `/<path>/` under `# git-subsync` |
| `.git/subsync/<path>.json` | no | For each file, the last synced version (Drive ID, MD5, size, mtime), plus the Drive folder IDs, the Drive folder name and the last sync time |
| `.git/subsync/<path>.lock` | no | Held while a sync runs |

```
[subsync "test-git-sync"]
	path = test-git-sync
	folder = 1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD
	mode = both
```

The state directory is `$(git rev-parse --git-path subsync)`, which is separate for each linked worktree. In state file names, `/` is escaped, e.g. `docs%2Fdrive.json`.

### Signing in

`add` and `sync` sign you in first if you aren't logged in or your session has expired. When stdin or stderr isn't a terminal, e.g. in a script, they print `Not logged in. Run git subsync login.` instead of waiting for a browser.

1. `login` starts a temporary server on `127.0.0.1` on a random free port.
2. It opens Google's consent page in your browser and also prints the URL in case the browser doesn't open.
3. Google redirects back to `http://127.0.0.1:<port>/callback`. git-subsync checks the random `state` value and accepts only the first callback.
4. It exchanges the code for a token using the PKCE (S256) verifier.

The flow times out after 3 minutes. The browser has to run on the same machine, because that's where Google redirects.

git-subsync requests offline access with `prompt=consent`, so Google always returns a refresh token. It asks for full Google Drive access (`https://www.googleapis.com/auth/drive`), because it opens existing Drive folders by URL, and the narrower `drive.file` scope can't do that.

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
| `Not inside a Git repository.` | Run git-subsync from inside the repo you want to sync. |
| `Drive folder <id> doesn't exist, or <email> has no access to it` | Check the link, and that the folder is shared with that account. `git subsync whoami` shows which account is signed in. |
| `warning: ... can't add files to ...` | Your account can only view the folder. Use `--mode download`, or ask for edit access. |
| `another git-subsync process (pid N) is syncing this folder` | Wait for it to finish. If that process isn't running, delete the lock file named in the message. |
| `... already has files but has never been synced with Drive folder ...` | Check that the Drive folder in `.gitsubsync` is the one you expect, then run `git subsync sync <path>` in a terminal and answer `y`. See [Syncing](#syncing). |
| `sync` exits with code 2 | Upload-mode conflicts. See [Conflicts](#conflicts). |
| A deleted file comes back | Deletes aren't synced yet. Delete it on both sides before the next sync. |

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
- the sync planner: every row of the sync table in all three modes, with and without `--force`, plus conflict copy names and names that can't be synced
- Drive folder link parsing, `.gitsubsync` and `.gitignore` editing (checked with `git status`), and the state file and lock
- the Drive API client against a fake HTTP server: pagination, retries, multipart and resumable uploads
- the executor against an in-memory Drive: both download and upload checksums, files that change mid-sync, and resuming after a failure
- `add`, `sync`, `list` and `remove` end to end, in a temp Git repo with the in-memory Drive. This includes a fresh clone and the upload-mode exit code 2.

The real Google sign-in and real Drive syncing are tested by hand before a release:
1. Add a real folder.
2. Edit a file locally and on Drive, then sync.
3. Create a conflict in each mode.
4. Sync from a fresh clone.

| Package | Purpose |
|---------|---------|
| `cmd/git-subsync` | entry point (handles Ctrl-C and exit codes) |
| `internal/cli` | cobra commands. Maps errors to messages like "Not logged in" and "Session expired". |
| `internal/auth` | OAuth flow, token store, client config, token revocation |
| `internal/gdrive` | Drive interface, API client with retries, folder link parsing |
| `internal/gdrive/drivetest` | in-memory Drive for tests |
| `internal/repo` | Git root, `.gitsubsync`, `.gitignore` |
| `internal/state` | sync state files and the lock |
| `internal/syncer` | local and Drive scans, the planner (a pure function) and the executor |
| `internal/version` | build metadata injected via `-ldflags` |

Only `internal/cli` imports cobra.

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

### v0.2.1 (2026-10-16)

- Emoji in the sync output (📥 📤 🔀 🛑 ⏩ ❌ ✅) and on `add`, `remove` and sign-in messages.
- A live status line on terminals: file counts while scanning, then a progress bar with the current file, percentage, bytes, speed and file count.
- Commands detect terminals properly, so `</dev/null` no longer counts as one.

### v0.2.0 (2026-10-16)

- `add`, `sync`, `list` and `remove` commands, with a committed `.gitsubsync` file and `.gitignore` entries.
- `both`, `download` and `upload` modes. Conflicts keep one version as a `_copy_` file. In upload mode, conflicts are reported with who changed the file and when, exit with code 2, and can be resolved with `sync --force`.
- `sync --dry-run`.
- Downloads and uploads are checked against Drive's MD5. Sync state is saved after every file, and a lock file stops two syncs of the same folder from running at once. Rate limits and server errors are retried.
- Drive commands sign you in first when you're not logged in.
- `sync` asks before the first upload from a folder that already has files. `.gitsubsync` paths that lead outside the repo are refused.

### v0.1.0 (2026-10-16)

- First release: `login` (with `--no-browser`), `logout`, `whoami`, `version`.
- Loopback OAuth with PKCE and state validation, a 3-minute timeout, and offline access.
- Token store with `0700`/`0600` permissions; refreshed tokens are written back to disk.
- Session-expiry detection (`invalid_grant`) with an actionable message.
- GoReleaser and GitHub Actions release to GitHub Releases and a Homebrew cask. It was first published as `tae-subsync` and renamed to `git-subsync` the same day. The old name still resolves through the tap's `cask_renames.json`; Homebrew 7 asks you to run `brew trust --cask tanawat-chanhom/tap/git-subsync` first.

## License

[MIT](LICENSE)
