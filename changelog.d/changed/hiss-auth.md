- **auth/** — HISS burn-down: constructors that panicked on missing configuration now return an error, `crypto/rand` failures are surfaced instead of ignored, and HTTP response bodies close through `core/errors.CloseInto`. Callers must handle the new error return:

  ```go
  // before
  svc := apikey.New(store, apikey.Options{HMACSecret: secret})
  signer := jwt.NewHMACSigner(jwt.HS256, secret, time.Hour)
  svc := magiclink.New(store, clk, secret, ttl)
  svc := recovery.New(codes, tokens, clk, secret)
  srv := oauth2server.New(opts)
  mw := impersonate.Middleware(opts)
  url, verifier := provider.AuthURL(state)

  // after
  svc, err := apikey.New(store, apikey.Options{HMACSecret: secret})       // error on empty HMACSecret
  signer, err := jwt.NewHMACSigner(jwt.HS256, secret, time.Hour)          // error on empty secret
  svc, err := magiclink.New(store, clk, secret, ttl)                      // error on empty secret
  svc, err := recovery.New(codes, tokens, clk, secret)                    // error on empty secret
  srv, err := oauth2server.New(opts)                                      // error on missing Issuer/Clients/Codes/Signer/Authenticate
  mw, err := impersonate.Middleware(opts)                                 // error on nil SessionGet/SessionSet
  url, verifier, err := provider.AuthURL(state)                           // error only when the OS entropy source fails
  ```

  `auth/session.Manager.Load` now returns an error (instead of a session with an empty ID) when generating a fresh session ID fails, and `session.MemoryStore` reports JSON round-trip failures. `auth/oauth2server` `/token` answers `server_error` when the JWT `jti` cannot be generated.
