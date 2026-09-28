---
name: auth
summary: Accounts, login flows, tokens for CI, and how the active account is chosen
---

# Authentication and accounts

## Logging in

```sh
{{app}} login                 # sign in via the browser, or a device code on remote machines
{{app}} login work --env prod # name the account "work"
{{app}} login --device        # force the device-code flow
{{app}} login ci --with-token --product cms < token.txt   # store an API token (such as a CMS integration token)
```

- A login needs a human to finish it in a browser. An agent must not try to complete one; ask the user to run `{{app}} login` instead.
- Credentials are stored in the OS keyring. On machines without a keyring, `--insecure-storage` stores them in a file with mode 0600.
- Access tokens are refreshed automatically. Commands never need a token passed to them.

## Several accounts

```sh
{{app}} account list          # the active account is marked with ●
{{app}} use personal          # switch the active account
{{app}} whoami --json         # show which account applies here and why
{{app}} logout work
```

The account for a command is chosen in this order:

1. The `--account <name>` flag
2. The `REEARTH_TOKEN` environment variable (an anonymous token account, meant for CI)
3. The `REEARTH_ACCOUNT` environment variable
4. The `account:` key in `.reearth.yaml`, searched upward from the current directory
5. The active account (`{{app}} use`)

`REEARTH_<PRODUCT>_TOKEN` (for example `REEARTH_CMS_TOKEN`) overrides the token for that product only.

## Checking credentials

```sh
{{app}} auth status --json
```

If a command exits with code 4, the session has expired or no account is logged in. Ask the user to run `{{app}} login <account>`.
