# Contributing to the Re:Earth CLI

This guide explains how the CLI is designed and how to extend it. It covers the ideas behind the code. The code itself shows the details.

## Design principles

The CLI takes its cues from `gh`, `gcloud`, `stripe`, `wrangler`, `docker` and `vercel`.

1. **stdout carries data; stderr carries everything else.** Spinners, progress messages, hints, warnings and errors go to stderr, so a pipeline never receives decoration.
2. **Humans and programs get the same data.** On a TTY, output is a styled table. Otherwise it is plain tab-separated rows. `--json`, `--jq` and `-o` produce structured output in every environment. A command builds its data once, and the SDK decides how to render it.
3. **Coding agents are first-class users.** The CLI detects agents (Claude Code, Cursor, Codex, ...) and CI. In those environments it never prompts, shows no spinners or update notices, and every failure carries a stable error code and exit code.
4. **Products are plugins on a shared SDK.** Authentication, accounts, output, configuration and HTTP live in `sdk/`. A product only describes its commands.
5. **Products never see credentials.** A product is given only an authenticated `*http.Client` (`f.HTTPClient`). The SDK hands products no token strings, so product code has no tokens to leak. `f.Auth` returns them and is for core commands only; products must not call it.
6. **Built-in commands cannot be shadowed.** Extensions cannot take the name of a built-in command. They are not sandboxed: an extension runs with the user's own access, like any program the user runs. The CLI does not hand them the credentials it stores, but that is not a security boundary. Users are protected by installing only extensions they trust.
7. **The binary documents itself.** `search`, `--help` and help topics are built from the command tree, so they always match the installed version.
8. **Agents search; they do not crawl.** `reearth search "<task>"` ranks every command offline. Walking `--help` level by level costs an agent one call per level, so the help of a command group tells agents to search instead.

## Architecture

```
cmd/reearth/         the distributed binary: every product, plus upgrade and extensions
cmd/reearth-<name>/  standalone product binaries (go install only; not distributed)
sdk/                 the library that products build on
  app/               assembles products and core commands into a CLI
  core/              the Product interface and the Factory passed to commands
  corecmd/           login, account, auth, api, config, search, help topics, skills, doctor, version
  auth/ credstore/   login flows, accounts, token refresh, keyring
  config/            user config (~/.config/reearth) and project files (.reearth.yaml)
  output/ iostreams/ rendering, TTY / agent / CI detection, colors, spinners
  httpx/             authenticated HTTP transport with retries and debug traces
  skills/            the SKILL.md that `skills install` writes
  cmdtree/           the command tree as data: search, surface snapshot, description lint
  docs/              docs.reearth.io through llms-full.txt: download, cache, page split, search
products/<name>/     product commands
internal/            features of the distributed binary only: self-update, extensions
docs/                GitHub Pages (install.sh, served at cli.reearth.io)
```

Dependencies point one way only: `cmd` → `products` → `sdk`. `sdk` never imports `products` or `internal`, and products never import each other. golangci-lint (depguard) enforces this. A product that splits into subpackages needs a depguard rule of its own; see `.golangci.yml`.

### One product, two binaries

Every product runs both as `reearth <product>` and as a standalone `reearth-<product>`. Both are built from the same `core.Product`:

- `app.Run` mounts several products under `reearth`.
- `app.Main` makes a single product the root command and adds the shared commands (`login`, `account`, `skills`, ...).

Both binaries share the same config and keyring, so a login in one works in the other. Only `reearth` is distributed. Invoking it through a symlink named `reearth-<product>` runs it exactly like the standalone binary (busybox-style dispatch). Commands of the distributed binary only, such as `upgrade` and `extension`, are not available there.

All product CLIs live in this repository, in one Go module. Each product's API client comes from that product's own Go SDK, such as `github.com/reearth/reearth-cms-api/go`.

## Accounts and authentication

### Login

- The browser flow is the authorization code flow with **PKCE (S256), which is mandatory**, and a loopback redirect to `127.0.0.1` on a random port.
- The device flow (RFC 8628) is used where a browser on the same machine cannot reach the loopback address: over SSH, in containers, in Codespaces, and on headless Linux. `--web` and `--device` override the detection.
- Both flows use `golang.org/x/oauth2`. `github.com/cli/oauth` is not used: its browser flow sends no PKCE parameters and is specific to GitHub's token endpoint, and its token type drops `expires_in` and `id_token`.

Auth0 settings for the CLI application:

- Application type: Native (a public client with no secret)
- Callback URL: `http://127.0.0.1/callback` (Auth0 accepts any port for loopback addresses)
- Grants: Authorization Code, Device Code and Refresh Token, with refresh token rotation enabled

The client ID of a public client is not a secret. It is still injected at build time (`-ldflags -X`) from repository secrets, so that the values for each environment are kept in one place. In development builds (see [Development](#development)), `REEARTH_AUTH_DOMAIN`, `REEARTH_AUTH_CLIENT_ID` and `REEARTH_AUTH_AUDIENCE` override these values for every environment. Release builds ignore these variables. Users can define other environments, such as on-premises installations, under `envs:` in the config file.

### Storing credentials

- Secrets live only in the OS keyring. Stored values are the refresh token (or the static token of a token account) and a best-effort cache of access tokens.
- `config.yaml` contains no secrets, and `.reearth.yaml` refuses keys that look like secrets, so that project files are safe to commit.
- A plain file is used instead of the keyring only when the user opts in with `--insecure-storage`. Writes to it run under a file lock and replace the file atomically, so that processes writing different accounts do not lose each other's updates.
- Keyrings cap the size of an entry: Windows Credential Manager at 2560 bytes, and the macOS `security` command line at 4096 bytes. When an entry would exceed the cap, the access token cache is dropped and the refresh token is kept.

### Refreshing tokens

Tokens are refreshed transparently inside the HTTP transport. Auth0 rotates refresh tokens, so two processes refreshing at the same time would invalidate each other's tokens. Refreshes therefore run under a per-account file lock, and the stored credentials are read again once the lock is held. When a request gets a 401 response, the transport refreshes the token and retries the request once. A static token cannot be refreshed, so its 401 response is returned as is. A failed refresh is never retried. When a refresh fails with `invalid_grant`, the command exits with code 4 and tells the user to log in again. If the rotated refresh token cannot be stored, the session is lost, because the server has already invalidated the old one; the command exits with code 4 (`auth.save_failed`). `auth status` and `doctor` force a refresh, so that a session revoked on the server is reported even while a cached access token is still valid.

An authenticated client refuses redirects to another scheme or host, because the transport would attach the token to the redirected request.

### Selecting the account

Each command resolves its account in this order: `--account`, `REEARTH_TOKEN` (an anonymous token account for CI), `REEARTH_ACCOUNT`, `account:` in `.reearth.yaml`, and finally the active account. `REEARTH_<PRODUCT>_TOKEN` overrides the token for one product only. A token account can be restricted to a single product (`login --with-token --product cms`), and its token is never sent to any other product.

### Audiences

An Auth0 access token is valid for a single audience, but each product server currently uses its own audience. The plan is a common Re:Earth API audience, accepted by every product server alongside the audience it already uses. This requires coordination with each product team. Until then, token caches are keyed by audience so that a product can use its own audience if needed.

The CMS integration API does not yet accept user JWTs (it has no JWT middleware), so the CMS product starts with token accounts that use integration tokens.

## Output conventions

- Commands call `f.Printer()` and pass their data to `p.Print(data, human)`. The data is what `--json` emits. `human` renders the table or plain output and is not called in machine formats. Field selection (`--json=a,b`), `--jq`, YAML and NDJSON are handled by the SDK.
- Treat JSON field names as a public API. Once a field ships, do not rename it.
- Times are relative on a TTY ("2 hours ago") and RFC 3339 elsewhere (`p.Time`). Long IDs are shortened on a TTY only (`p.ShortID`).
- Errors are `*cmdutil.Error` values with a stable `code`, a `message` and an optional `hint`. Their exit codes are 1 (general error), 2 (invalid usage), 4 (authentication), 5 (not found) and 8 (cancelled). With `--json`, `--jq` or `-o json|ndjson`, errors are printed to stderr as JSON, even when the flags themselves fail to parse.
- Only prompt when `f.IO.CanPrompt()` is true. Otherwise fail with an error that names the flag to pass. Destructive actions go through `f.Confirm`, which honors `--yes`.

## Look and feel

The human output aims for a calm, polished CLI in the style of Vercel:

- No borders. Hierarchy comes from whitespace, bold text and dimmed secondary text.
- One brand accent color. Status icons carry meaning: `✓` success, `✗` failure, `!` warning, `→` in progress, `●` active. Icons remain when `NO_COLOR` is set.
- Two-space left margin on a TTY. Uppercase, dimmed table headers.
- Update notices appear once, as the last line after a command finishes, and never in machine output.

Use the helpers in `iostreams` (`Success`, `Warn`, `Info`, `Hint`, `StartProgress`) and `output.Table` rather than printing escape codes directly.

## Docs for agents

Agents learn the CLI from the CLI itself. There are no separate docs to keep in sync:

- `reearth search "<task>"` finds a command. `<command> --help` shows its flags and examples.
- A command's `Long` carries what an agent needs beyond the flags: concepts, the order in which values are resolved, and what to do when the command fails. `search` matches it too, and the `Long` of a group leads to the group's commands.
- Knowledge that belongs to no single command is a help topic (`reearth help exit-codes`): a text file in `sdk/corecmd/topics/` registered in `topics.go`. Write `{{app}}` for the binary name.
- Knowledge about the products themselves, such as what a reference field is, lives at docs.reearth.io. `reearth docs search` and `docs read` read it through the site's `llms-full.txt` (`sdk/docs`), so do not copy it into `Long`; link the page instead.
- `reearth docs read` takes an id or a title. Ids are page URL paths when the site publishes page URLs, and titles otherwise; then pages that share a title get numbered ids (`概要 (1)`, `概要 (2)`), so every id names one page. A title that several pages share is ambiguous and exits with 2, listing the ids. The numbers follow the order of `llms-full.txt`, so they can change when pages are added; page URLs work once the site publishes them.
- `SKILL.md` (`sdk/skills/SKILL.md.tmpl`) holds only what outlives a release: how to find commands, and the rules for output, prompts, login and `--yes`. It names no product or command flags, only global ones that are part of the CLI's stable contract (`--json`, `--jq`, `--yes`, `--help`), so an old installed copy never teaches outdated ones. The help topics it names are checked against `topics.go` by a test.

## Command descriptions and the command surface

`search`, `--help` and the help topics all read the same descriptions, so they are held to a few rules. `cmdtree.Lint` checks them in `cmd/reearth/main_test.go`:

- Every visible command has a `Short` of two words or more that says what the command does. A summary that repeats the name, such as `dns` or `Operations for records`, cannot be found by describing a task. Without generic words (`manage`, `operations`, `commands`, `work with`, `for`, `the`, ...), a `Short` must say more than the command's name or its parent's name: `Manage accounts` on `account` fails, `List accounts` on `account list` passes.
- `Short` and flag usages are one line, start with a capital letter and have no trailing period. Put details in `Long`; it is searched too.
- cobra's completion commands and the flags cobra adds (`--version`) are not linted: cobra writes their text.

`cmd/reearth/testdata/commands.json` pins every command, alias, argument and flag of the distributed binary, including the `completion` command and `--version` that cobra adds when the CLI runs (`cmdtree.InitDefaults`). When a change touches the command tree, run `make golden` and commit the diff. Reviewers read that diff to spot renamed or removed commands and flags, which break users' scripts.

## Extensions and self-update

Extensions follow the `gh extension` model with tighter rules. Binaries named `reearth-*` on `PATH` are never run implicitly, because any file that lands on `PATH` would run under a trusted name. The following rules apply:

- Only extensions installed with `reearth ext install` run.
- The SHA-256 of each installed binary is pinned and verified before every run.
- Extensions from owners other than `reearth` are labeled third-party.
- Extensions are not sandboxed. They run with the user's own access, like any program the user runs, and inherit environment variables, including `REEARTH_TOKEN`. The CLI does not hand them the credentials it stores (keyring or file); an extension that needs the API calls `$REEARTH_BIN api`. This prevents accidental exposure only: a malicious extension can read the keyring itself. What protects users is installing only extensions they trust, which the pinned SHA-256 and the official or third-party label support.
- Extensions run with `REEARTH_EXTENSION=1`. The CLI uses it to recognise a call from an extension and refuses `auth token` then. An extension can unset the variable, so this guards against mistakes, not against a malicious extension.
- If a release publishes a checksums file, it must list the binary and the download must match. Only a release without any checksums file installs unverified, after confirmation.
- When an agent is detected, extensions run only through the explicit `reearth ext exec`.

`reearth upgrade` updates only the `reearth` binary, because every product ships inside it. If a package manager installed the binary, `upgrade` prints that package manager's upgrade command instead of replacing the binary. Downloads are verified against the release's checksums file.

## Adding a product

1. Create `products/<name>/` and implement `core.Product`:

   ```go
   type Product struct{}

   func (Product) Name() string  { return "cms" }
   func (Product) Short() string { return "Manage Re:Earth CMS" }
   func (p Product) Command(f *core.Factory) *cobra.Command { /* the product's command tree */ }
   ```

2. If the product calls an API, implement `core.APIProduct` by adding `BaseURL(env *auth.Env) string`. Inside commands:
   - Get a client with `f.HTTPClient(ctx, p)` and the URL with `f.BaseURL(p)`. Users can override the URL with `REEARTH_<PRODUCT>_BASE_URL`.
   - Read project-scoped defaults, such as a CMS project, with `f.ProjectValue(p, "project", flagValue)`. The value comes from the flag, then `REEARTH_<PRODUCT>_PROJECT`, then `.reearth.yaml`.
   - The raw `reearth api <product> <path>` command works automatically.

3. Explain the product's concepts, such as how projects, models and items relate, in the `Long` of its root command. Agents read it with `--help`, and `search` uses it to rank the product's commands.

4. Register the product in `cmd/reearth/main.go`. Optionally, add `cmd/reearth-<name>/main.go`, which contains only `app.Main(<name>.Product{})`.

5. Write examples as `$ reearth <product> ...`. They are rewritten automatically for the standalone binary.

6. Add tests. `sdk/app/app_test.go` shows how to run commands end to end against fake servers, and `products/hello` is a minimal reference product.

## Development

```sh
make build     # ./bin/reearth
make test
make lint      # golangci-lint v2
make snapshot  # cross-build every release artifact into ./dist
```

A build is a release build when its version is a release version: injected by GoReleaser, or stamped by Go for `go install …@vX.Y.Z` or a `go build` at a clean tagged commit. Anything else is a development build: `dev`, a snapshot, a pseudo-version that Go stamps when building at an untagged commit, or a build from a modified checkout (`+dirty`). A development build shows no update notices, and `upgrade` replaces it only with `--force`.

To sign in with a development build, point it at an Auth0 tenant:

```sh
export REEARTH_AUTH_DOMAIN=example.auth0.com REEARTH_AUTH_CLIENT_ID=xxx REEARTH_AUTH_AUDIENCE=https://api.example
```

To keep a development build away from your real config, set `REEARTH_CONFIG_DIR`, `REEARTH_CACHE_DIR` and `REEARTH_DATA_DIR`. `reearth doctor` diagnoses the config, the keyring, network access, clock skew and the installation.

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/). The release notes are generated from them. `feat`, `fix` and `perf` commits, and breaking changes marked with `!`, get their own sections. `docs`, `test`, `ci` and `chore` commits are left out.

## Releasing

Push a `vX.Y.Z` tag. The release workflow runs GoReleaser, which:

- builds the archives and the deb, rpm and apk packages
- signs the checksums file with cosign (keyless)
- adds build provenance attestations
- writes the release notes
- updates the Homebrew cask in `reearth/homebrew-tap` and the Scoop manifest in `reearth/scoop-bucket`
- submits the winget manifest to `microsoft/winget-pkgs` (when `WINGET_TOKEN` is set)

Repository secrets:

| Secret | Purpose |
|---|---|
| `REEARTH_AUTH0_DOMAIN`, `REEARTH_AUTH0_CLIENT_ID`, `REEARTH_AUTH0_AUDIENCE` | Production Auth0 native app (required; the workflow stops without them) |
| `REEARTH_AUTH0_STAGING_DOMAIN`, `..._CLIENT_ID`, `..._AUDIENCE` | Built-in `--env staging` (optional) |
| `WINGET_TOKEN` | Classic token with `public_repo` scope from an account that can push to `reearth/winget-pkgs` (optional) |

The Homebrew cask and the Scoop manifest are pushed with a token minted from the org's `reearth-app` GitHub App (`vars.GH_APP_ID`, `secrets.GH_APP_PRIVATE_KEY`, `vars.GH_APP_USER`) and scoped to `homebrew-tap` and `scoop-bucket`. winget publishing pushes a branch to `reearth/winget-pkgs` (a fork of `microsoft/winget-pkgs`) and opens a pull request upstream. The app is not installed on `microsoft/winget-pkgs` and cannot open that pull request, so winget needs a user token in the `WINGET_TOKEN` secret, and it is skipped while the secret is unset.

All GitHub Actions are pinned to commit SHAs, and Dependabot keeps the pins up to date.
