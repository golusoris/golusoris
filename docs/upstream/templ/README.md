# a-h/templ — v0.3.898 snapshot

Pinned: **v0.3.898**
Source: https://pkg.go.dev/github.com/a-h/templ@v0.3.898
Docs: https://templ.guide

## Template definition

```templ
// file: views/user.templ
package views

templ UserCard(name string, email string) {
  <div class="card">
    <h2>{ name }</h2>
    <p>{ email }</p>
  </div>
}

templ Page(title string, content templ.Component) {
  <!DOCTYPE html>
  <html>
    <head><title>{ title }</title></head>
    <body>
      @content
    </body>
  </html>
}
```

## Code generation

```sh
templ generate          # generates views/user_templ.go
templ generate --watch  # watch mode for development
```

## Rendering

```go
// To http.ResponseWriter
err := views.Page("Home", views.UserCard("Alice", "alice@example.com")).Render(ctx, w)

// To string/bytes
var buf bytes.Buffer
err := views.UserCard("Alice", "alice@example.com").Render(ctx, &buf)
html := buf.String()
```

## Handler pattern

```go
func UserHandler(w http.ResponseWriter, r *http.Request) {
    user := getUser(r)
    component := views.UserCard(user.Name, user.Email)
    if err := component.Render(r.Context(), w); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}
```

## HTMX integration

```templ
templ UserRow(user User) {
  <tr hx-get={ "/users/" + user.ID } hx-target="#detail">
    <td>{ user.Name }</td>
  </tr>
}
```

## golusoris usage

- `htmltmpl/` — templ component rendering helpers + chi handler adapter.

## Security note

templ auto-escapes all `{ expression }` content. Raw HTML requires `templ.Raw(html)` — use only with trusted content.

## Links

- Changelog: https://github.com/a-h/templ/blob/main/CHANGELOG.md
