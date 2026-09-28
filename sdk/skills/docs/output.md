---
name: output
summary: Output formats (--json, --jq, -o), errors on stderr, and exit codes
---

# Output and exit codes

## Formats

| Flag | Result |
|---|---|
| none, on a TTY | A table for humans, with relative times and shortened IDs |
| none, without a TTY | Tab-separated plain rows with no header |
| `--json` | JSON with every field, full IDs and RFC 3339 times |
| `--json=id,name` | JSON with only the listed fields |
| `--jq '.[] .id'` | The JSON filtered with jq. String results are printed raw |
| `-o yaml` / `-o ndjson` | YAML, or one JSON value per line |

Always parse `--json` output rather than the table, which can change at any time.

## Errors

Errors are written to stderr. With `--json`, they are also structured:

```json
{"error":{"code":"auth.reauth_required","message":"...","hint":"run `{{app}} login work`"}}
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General error |
| 2 | Invalid usage, or input required but prompting is not possible |
| 4 | Authentication required or expired |
| 5 | Not found |
| 8 | Cancelled by the user |
