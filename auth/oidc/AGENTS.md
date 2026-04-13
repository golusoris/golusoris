# Agent guide — auth/oidc/

OIDC + OAuth 2.0 PKCE client via [coreos/go-oidc/v3]. Module provides
`*oidc.Provider` to the fx graph after running OIDC discovery.

## Flow

```
1. handler: url, verifier := provider.AuthURL(state) → store verifier in session → redirect
2. callback: set, err := provider.Exchange(ctx, code, verifier)
3. callback: info, err := provider.UserInfo(ctx, set.AccessToken)
```

## Config

```
auth.oidc.issuer_url    = "https://accounts.google.com"
auth.oidc.client_id     = "…"
auth.oidc.client_secret = "…"
auth.oidc.redirect_url  = "https://app.example.com/auth/callback"
auth.oidc.scopes        = ["openid", "email", "profile"]
```

## PKCE

PKCE (S256) is always on. `AuthURL` returns the verifier; store it in
`auth/session` under a key like `"pkce_verifier"` before redirecting.
Pass it back to `Exchange` on the callback.

## Don't

- Don't skip state validation — always verify the `state` param on callback matches what you set.
- Don't store the raw id_token in a cookie — store it server-side in `auth/session`.
- Don't call `UserInfo` on every request — cache the result in `cache/memory` or `auth/session`.
