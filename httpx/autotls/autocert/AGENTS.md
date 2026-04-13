# Agent guide — httpx/autotls/autocert

x/crypto/acme/autocert wrapper.

## Conventions

- `http.autotls.autocert.domains` is required + explicit — autocert refuses to issue for arbitrary hosts.
- `http.autotls.autocert.cache` directory must be writable + persistent across restarts (certs are cached there).
- Let's Encrypt's ACME TOS is auto-accepted via `AcceptTOS`.

## Don't

- Don't use autocert with a non-persistent cache (emptyDir, tmpfs). Let's Encrypt rate-limits issuance; losing the cache rate-limits you fast.
