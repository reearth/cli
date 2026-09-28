# Re:Earth CLI

`reearth` is the command-line interface for [Re:Earth](https://reearth.io). Each Re:Earth product (Visualizer, CMS, Flow) is a subcommand, and all of them share accounts, output formats and docs.

```sh
reearth login
reearth hello world
reearth hello me --json
```

## Install

```sh
brew install reearth/tap/reearth                    # macOS / Linux
winget install Reearth.Reearth                      # Windows
scoop bucket add reearth https://github.com/reearth/scoop-bucket
scoop install reearth                               # Windows
curl -fsSL https://cli.reearth.io/install.sh | sh   # macOS / Linux, no package manager
```

`.deb`, `.rpm` and `.apk` packages are attached to each [release](https://github.com/reearth/cli/releases).

Run `reearth upgrade` to update. If you installed with a package manager, the command tells you how to upgrade with it.

## Usage

| Command | Description |
|---|---|
| `reearth login [account]` | Sign in. Uses the browser (authorization code + PKCE on a loopback address), or a device code over SSH or in containers |
| `reearth logout [account]` / `use <account>` | Sign out, or switch the active account |
| `reearth account list` / `whoami` | List accounts, and show which one applies here and why |
| `reearth auth status` | Check that each account's credentials work |
| `reearth api <product> <path>` | Send a raw authenticated API request |
| `reearth config get/set/list` | Read and change settings |
| `reearth skills [doc]` / `skills install` | Read docs for coding agents, or install the agent skill |
| `reearth extension install/list/exec` | Manage extensions |
| `reearth doctor` | Diagnose configuration, the keyring, the network and the installation |
| `reearth upgrade` | Update the CLI |

Global flags: `--account`, `--json[=fields]`, `--jq`, `-o table|plain|json|yaml|ndjson`, `--yes`, `--no-input`, `--no-color`, `--quiet`, `--debug`.

Credentials are stored in the OS keyring (macOS Keychain, Windows Credential Manager, or Secret Service). On machines without a keyring, pass `--insecure-storage`. For CI, set `REEARTH_TOKEN`.

## Development

```sh
make build     # ./bin/reearth
make test
make lint
make snapshot  # cross-build every release artifact into ./dist
```

To sign in with a development build, point it at an Auth0 tenant:

```sh
export REEARTH_AUTH_DOMAIN=example.auth0.com REEARTH_AUTH_CLIENT_ID=xxx REEARTH_AUTH_AUDIENCE=https://api.example
```

### Layout

```
cmd/reearth/         the distributed binary: all products + upgrade + extensions
cmd/reearth-<p>/     standalone product binaries (go install only)
sdk/                 the library that products build on
  app/               assembles products and core commands into a CLI
  core/              the Product interface and the Factory passed to commands
  corecmd/           login, account, auth, api, config, skills, doctor, version
  auth/ credstore/   login flows, accounts, token refresh, keyring
  output/ iostreams/ human and machine output, TTY and agent detection
  skills/            agent docs embedded in the binary
products/<name>/     product commands (hello is a sample)
internal/            self-update and extensions (binary-specific)
docs/                GitHub Pages: install.sh
```

### Adding a product

Implement `core.Product` in `products/<name>`, add it to `cmd/reearth/main.go`, and optionally add `cmd/reearth-<name>/main.go`:

```go
type Product struct{}

func (Product) Name() string  { return "cms" }
func (Product) Short() string { return "Manage Re:Earth CMS" }
func (Product) Command(f *core.Factory) *cobra.Command { /* ... */ }

// Optional:
func (Product) BaseURL(env *auth.Env) string { return "https://api.cms.reearth.io/api" } // core.APIProduct
func (Product) Skills() fs.FS { /* embedded markdown */ }                                // core.SkillsProduct
```

Inside commands, call `f.HTTPClient(ctx, product)` to get an authenticated client, and `f.Printer()` to print output that respects `--json`, `--jq` and `-o`.

## Releasing

Push a `vX.Y.Z` tag. The release workflow runs GoReleaser, which builds the archives and packages, signs the checksums with cosign (keyless), adds build provenance attestations, writes the changelog from Conventional Commits, and updates the Homebrew tap.

Repository secrets:

| Secret | Purpose |
|---|---|
| `REEARTH_AUTH0_DOMAIN`, `REEARTH_AUTH0_CLIENT_ID`, `REEARTH_AUTH0_AUDIENCE` | Production Auth0 native app (required) |
| `REEARTH_AUTH0_STAGING_DOMAIN`, `..._CLIENT_ID`, `..._AUDIENCE` | Staging environment (optional) |

The cask is pushed to `reearth/homebrew-tap` with a token minted from the org's `reearth-app` GitHub App (`vars.GH_APP_ID`, `secrets.GH_APP_PRIVATE_KEY`, `vars.GH_APP_USER`). Scoop and winget publishing is configured but skipped until `reearth/scoop-bucket` and a `winget-pkgs` fork exist.
