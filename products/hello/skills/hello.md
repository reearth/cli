---
name: hello
summary: Sample product — greetings and an authenticated API call
---

# hello

`{{cmd}}` is a sample product. It shows how product commands behave:

- `{{cmd}} world [name]` prints a greeting. It needs no login.
- `{{cmd}} me` calls an API as the current account and prints your profile. It exits with code 4 if you are not logged in.

Both commands support `--json`.
