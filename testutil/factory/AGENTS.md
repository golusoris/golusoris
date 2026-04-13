# Agent guide — testutil/factory/

Deterministic test data factory backed by brianvoe/gofakeit v6.

`factory.New(t)` seeds the faker from `t.Name()` so the same test always
produces the same data. This makes snapshot tests + golden-file comparisons
stable across runs.

## Usage

```go
func TestCreateUser(t *testing.T) {
    f := factory.New(t) // deterministic for this test
    user := User{
        Email: f.Email(),
        Name:  f.Name(),
        Age:   f.Number(18, 80),
    }
    // ...
}

// For non-deterministic data (e.g. load tests):
f := factory.Random()
```

## Available generators (via *gofakeit.Faker)

`Email`, `Name`, `FirstName`, `LastName`, `UUID`, `URL`, `IPv4Address`,
`CreditCardNumber`, `Password`, `LoremIpsum`, `Number`, `Float64`, `Bool`,
`Date`, `PhoneFormatted`, `Company`, `JobTitle`, `Username`, `Color`, etc.

Full reference: https://pkg.go.dev/github.com/brianvoe/gofakeit/v6

## Don't

- Don't share a single `*Faker` across parallel sub-tests — each sub-test
  should call `factory.New(t)` with its own `*testing.T`.
