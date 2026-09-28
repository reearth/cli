---
name: overview
summary: What the CLI does, how commands are organized, and where to start
---

# {{app}} — overview

`{{app}}` is the command-line interface for Re:Earth. Each Re:Earth product is a subcommand:

```sh
{{app}} <product> <resource> <action> [flags]
```

Commands shared by every product:

| Command | Purpose |
|---|---|
| `{{app}} login` / `logout` / `use` | Sign in, sign out, and switch between accounts |
| `{{app}} account list` / `whoami` | Show the accounts and which one is active |
| `{{app}} auth status` | Check whether each account's credentials still work |
| `{{app}} api <product> <path>` | Send a raw authenticated HTTP request to a product API |
| `{{app}} config get/set/list` | Read and change CLI settings |
| `{{app}} skills <doc>` | Read these docs |

Run `{{app}} skills list` to see every doc, including the ones for each product.

## Principles for automation

- stdout carries only data. Progress messages, hints and warnings go to stderr.
- `--json` switches any command to structured output. `--json=a,b` keeps only the fields `a` and `b`, and `--jq '<expr>'` filters the result.
- Without a TTY, the CLI never prompts. A command that needs input fails with exit code 2 and tells you which flag to pass.
- Destructive commands ask for confirmation. Pass `--yes` only after the user has agreed.
- Use `--account <name>` to run a single command as a different account.

See `{{app}} skills output` and `{{app}} skills auth` for details.
