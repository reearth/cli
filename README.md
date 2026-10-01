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
| `reearth search "<task>"` | Find the command for a task |
| `reearth docs search/read` | Search and read the documentation at docs.reearth.io |
| `reearth help <topic>` | Read about output formats, exit codes and environment variables |
| `reearth skills install` | Install the skill that teaches coding agents to use the CLI |
| `reearth extension install/list/exec` | Manage extensions |
| `reearth doctor` | Diagnose configuration, the keyring, the network and the installation |
| `reearth upgrade` | Update the CLI |

Global flags: `--account`, `--json[=fields]`, `--jq`, `-o table|plain|json|yaml|ndjson`, `--yes`, `--no-input`, `--no-color`, `--quiet`, `--debug`.

Credentials are stored in the OS keyring (macOS Keychain, Windows Credential Manager, or Secret Service). On machines without a keyring, pass `--insecure-storage`. For CI, set `REEARTH_TOKEN`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the design, how to add a product, development setup and the release process.

## License

[MIT](LICENSE)
