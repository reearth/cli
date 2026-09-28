# Contributing to the Re:Earth CLI

This guide explains how the CLI is designed and how to extend it. It covers the ideas behind the code. The code itself shows the details.

## Design principles

The CLI takes its cues from `gh`, `gcloud`, `stripe`, `wrangler`, `docker` and `vercel`.

1. **stdout carries data; stderr carries everything else.** Spinners, progress messages, hints, warnings and errors go to stderr, so a pipeline never receives decoration.
2. **Humans and programs get the same data.** On a TTY, output is a styled table. Otherwise it is plain tab-separated rows. `--json`, `--jq` and `-o` produce structured output in every environment. A command builds its data once, and the SDK decides how to render it.
3. **Coding agents are first-class users.** The CLI detects agents (Claude Code, Cursor, Codex, ...) and CI. In those environments it never prompts, shows no spinners or update notices, and every failure carries a stable error code and exit code.
4. **Products are plugins on a shared SDK.** Authentication, accounts, output, configuration and HTTP live in `sdk/`. A product only describes its commands.
5. **Products never see credentials.** A product asks for an authenticated `*http.Client`. It never handles token strings, so tokens cannot leak through product code.
6. **Built-in commands cannot be shadowed.** Extensions run outside the trust boundary. They cannot take the name of a built-in command and do not receive credentials.
7. **Docs ship inside the binary.** Agent docs are embedded and partly generated from the command tree, so they always match the installed version.

## Architecture

```
cmd/reearth/         the distributed binary: every product, plus upgrade and extensions
cmd/reearth-<name>/  standalone product binaries (go install only; not distributed)
sdk/                 the library that products build on
  app/               assembles products and core commands into a CLI
  core/              the Product interface and the Factory passed to commands
  corecmd/           login, account, auth, api, config, skills, doctor, version
  auth/ credstore/   login flows, accounts, token refresh, keyring
  config/            user config (~/.config/reearth) and project files (.reearth.yaml)
  output/ iostreams/ rendering, TTY / agent / CI detection, colors, spinners
  httpx/             authenticated HTTP transport with retries and debug traces
  skills/            embedded agent docs
products/<name>/     product commands
internal/            features of the distributed binary only: self-update, extensions
docs/                GitHub Pages (install.sh, served at cli.reearth.io)
```

Dependencies point one way only: `cmd` → `products` → `sdk`. `sdk` never imports `products` or `internal`, and products never import each other. golangci-lint (depguard) enforces this.

### One product, two binaries

Every product runs both as `reearth <product>` and as a standalone `reearth-<product>`. Both are built from the same `core.Product`:

- `app.Run` mounts several products under `reearth`.
- `app.Main` makes a single product the root command and adds the shared commands (`login`, `account`, `skills`, ...).

Both binaries share the same config and keyring, so a login in one works in the other. Only `reearth` is distributed. Invoking it through a symlink named `reearth-<product>` gives the standalone experience (busybox-style dispatch).

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

The client ID of a public client is not a secret. It is still injected at build time (`-ldflags -X`) from repository secrets, so that the values for each environment are kept in one place. Development builds read `REEARTH_AUTH_DOMAIN`, `REEARTH_AUTH_CLIENT_ID` and `REEARTH_AUTH_AUDIENCE` instead. Users can define other environments, such as on-premises installations, under `envs:` in the config file.

### Storing credentials

- Secrets live only in the OS keyring. Stored values are the refresh token (or the static token of a token account) and a best-effort cache of access tokens.
- `config.yaml` contains no secrets, and `.reearth.yaml` refuses keys that look like secrets, so that project files are safe to commit.
- A plain file is used instead of the keyring only when the user opts in with `--insecure-storage`.
- Windows Credential Manager caps an entry at 2560 bytes. When an entry would exceed the cap, the access token cache is dropped and the refresh token is kept.

### Refreshing tokens

Tokens are refreshed transparently inside the HTTP transport. Auth0 rotates refresh tokens, so two processes refreshing at the same time would invalidate each other's tokens. Refreshes therefore run under a per-account file lock, and the stored credentials are read again once the lock is held. When a request gets a 401 response, the transport refreshes the token and retries the request once. When a refresh fails with `invalid_grant`, the command exits with code 4 and tells the user to log in again.

### Selecting the account

Each command resolves its account in this order: `--account`, `REEARTH_TOKEN` (an anonymous token account for CI), `REEARTH_ACCOUNT`, `account:` in `.reearth.yaml`, and finally the active account. `REEARTH_<PRODUCT>_TOKEN` overrides the token for one product only. A token account can be restricted to a single product (`login --with-token --product cms`), and its token is never sent to any other product.

### Audiences

An Auth0 access token is valid for a single audience, but each product server currently uses its own audience. The plan is a common Re:Earth API audience, accepted by every product server alongside the audience it already uses. This requires coordination with each product team. Until then, token caches are keyed by audience so that a product can use its own audience if needed.

The CMS integration API does not yet accept user JWTs (it has no JWT middleware), so the CMS product starts with token accounts that use integration tokens.

## Output conventions

- Commands call `f.Printer()` and pass their data to `p.Print(data, human)`. The data is what `--json` emits. `human` renders the table or plain output and is not called in machine formats. Field selection (`--json=a,b`), `--jq`, YAML and NDJSON are handled by the SDK.
- Treat JSON field names as a public API. Once a field ships, do not rename it.
- Times are relative on a TTY ("2 hours ago") and RFC 3339 elsewhere (`p.Time`). Long IDs are shortened on a TTY only (`p.ShortID`).
- Errors are `*cmdutil.Error` values with a stable `code`, a `message` and an optional `hint`. Their exit codes are 1 (general error), 2 (invalid usage), 4 (authentication), 5 (not found) and 8 (cancelled). With `--json`, errors are printed to stderr as JSON.
- Only prompt when `f.IO.CanPrompt()` is true. Otherwise fail with an error that names the flag to pass. Destructive actions go through `f.Confirm`, which honors `--yes`.

## Look and feel

The human output aims for a calm, polished CLI in the style of Vercel:

- No borders. Hierarchy comes from whitespace, bold text and dimmed secondary text.
- One brand accent color. Status icons carry meaning: `✓` success, `✗` failure, `!` warning, `→` in progress, `●` active. Icons remain when `NO_COLOR` is set.
- Two-space left margin on a TTY. Uppercase, dimmed table headers.
- Update notices appear once, as the last line after a command finishes, and never in machine output.

Use the helpers in `iostreams` (`Success`, `Warn`, `Info`, `Hint`, `StartProgress`) and `output.Table` rather than printing escape codes directly.

## Docs for agents

`SKILL.md` stays thin on purpose. It tells agents to run `reearth skills <doc>` and states a few rules. The docs themselves are embedded markdown files, and the reference for each product's commands is generated from its cobra tree when a doc is rendered. A stale SKILL.md therefore never teaches outdated flags.

- Core docs live in `sdk/skills/docs/`. Product docs live in `products/<name>/skills/`.
- Each doc starts with front matter containing a `name` and a one-line `summary`.
- In a doc body, write `{{app}}` for the binary name and `{{cmd}}` for the product's command prefix (`reearth cms` or `reearth-cms`).
- Write docs for agents: say what to run and what the exit codes mean. Leave flag listings to the generated reference.

## Extensions and self-update

Extensions follow the `gh extension` model with tighter rules. Binaries named `reearth-*` on `PATH` are never run implicitly, because any file that lands on `PATH` would run under a trusted name. The following rules apply:

- Only extensions installed with `reearth ext install` run.
- The SHA-256 of each installed binary is pinned and verified before every run.
- Extensions from owners other than `reearth` are labeled third-party.
- Extensions receive no credentials. An extension that needs the API calls `$REEARTH_BIN api`.
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

3. Add agent docs. Implement `core.SkillsProduct` by returning an embedded `fs.FS` of `skills/*.md`. The doc named after the product gets the generated command reference.

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
- updates the Homebrew cask in `reearth/homebrew-tap`

Repository secrets:

| Secret | Purpose |
|---|---|
| `REEARTH_AUTH0_DOMAIN`, `REEARTH_AUTH0_CLIENT_ID`, `REEARTH_AUTH0_AUDIENCE` | Production Auth0 native app (required; the workflow stops without them) |
| `REEARTH_AUTH0_STAGING_DOMAIN`, `..._CLIENT_ID`, `..._AUDIENCE` | Built-in `--env staging` (optional) |

The cask is pushed with a token minted from the org's `reearth-app` GitHub App (`vars.GH_APP_ID`, `secrets.GH_APP_PRIVATE_KEY`, `vars.GH_APP_USER`) and scoped to `homebrew-tap`. Scoop and winget publishing is configured but skipped until `reearth/scoop-bucket` and a `winget-pkgs` fork exist. At that point, add those repositories to the app token in the workflow.

All GitHub Actions are pinned to commit SHAs, and Dependabot keeps the pins up to date.
