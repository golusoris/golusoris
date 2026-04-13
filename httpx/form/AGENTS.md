# Agent guide — httpx/form

Decodes HTML form submissions into Go structs via go-playground/form/v4.

## Conventions

- App code calls `r.ParseForm()` then `dec.Decode(&req, r.PostForm)`.
- Struct tags: `form:"field_name"`. Missing-field errors flow through as `*gerr.Error` with `CodeBadRequest` — app handlers can return them directly through ogenkit / the JSON error path.
- Validation is a separate step: after Decode, run the struct through `validate.Validator` (in `golusoris/validate`).

## Don't

- Don't use this for JSON request bodies — ogen handles those. This is strictly for `application/x-www-form-urlencoded` + `multipart/form-data`.
