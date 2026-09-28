---
name: api
summary: Raw authenticated HTTP requests to product APIs with `api`
---

# Raw API access

`{{app}} api` sends an authenticated request to a product's API. Use it for endpoints that have no dedicated command yet.

```sh
{{app}} api <product> <path>                      # GET
{{app}} api <product> <path> -X POST -f name=x    # send -f fields as a JSON body
{{app}} api <product> <path> -F count=3           # -F sends typed JSON values (numbers, booleans, null)
{{app}} api <product> <path> --input body.json    # read the body from a file ("-" reads stdin)
{{app}} api <product> <path> --jq '.items[].id'
```

- `<path>` is resolved against the product's base URL. Override the base URL with `REEARTH_<PRODUCT>_BASE_URL`.
- For GET requests, `-f` / `-F` become query parameters.
- A non-2xx response prints the body and exits with code 1 (code 4 for 401 and 403).
- `-i` also prints the status line and the response headers.
