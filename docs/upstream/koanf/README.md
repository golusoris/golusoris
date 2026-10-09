<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# knadh/koanf/v2 — v2.3.8 snapshot

Pinned: **v2.3.8**
Source: [tagged source](https://github.com/knadh/koanf/tree/v2.3.8)

## Loading config

Use `github.com/knadh/koanf/providers/env/v2` and
`github.com/go-viper/mapstructure/v2`; their v1 call shapes are incompatible.

```go
k := koanf.New(".")

// Load from environment (prefix APP_, underscore-separated keys)
envProvider := env.Provider(".", env.Opt{
    Prefix: "APP_",
    TransformFunc: func(key, value string) (string, any) {
        key = strings.TrimPrefix(key, "APP_")
        return strings.ReplaceAll(strings.ToLower(key), "_", "."), value
    },
})
if err := k.Load(envProvider, nil); err != nil {
    return fmt.Errorf("load environment: %w", err)
}

// Load from YAML file
if err := k.Load(file.Provider("config.yaml"), yaml.Parser()); err != nil {
    return fmt.Errorf("load YAML: %w", err)
}

// Load from embedded FS
embeddedProvider := fs.Provider(embeddedFS, "config.yaml")
if err := k.Load(embeddedProvider, yaml.Parser()); err != nil {
    return fmt.Errorf("load embedded YAML: %w", err)
}
```

## Unmarshalling

```go
var cfg MyConfig
if err := k.Unmarshal("", &cfg); err != nil {
    return err
}

// With custom decode hooks (used in golusoris core/config/)
if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{
    Tag: "koanf",
    DecoderConfig: &mapstructure.DecoderConfig{
        DecodeHook: mapstructure.ComposeDecodeHookFunc(
            mapstructure.StringToTimeDurationHookFunc(),
            mapstructure.StringToSliceHookFunc(","),
        ),
        Metadata:         nil,
        Result:           &cfg,
        WeaklyTypedInput: true,
    },
}); err != nil {
    return err
}
```

## File-watch (hot reload)

```go
provider := file.Provider("config.yaml")
if err := provider.Watch(func(_ any, watchErr error) {
    if watchErr != nil {
        logger.Warn("config watch failed", slog.Any("err", watchErr))
        return
    }
    if err := k.Load(provider, yaml.Parser()); err != nil {
        logger.Warn("config reload failed", slog.Any("err", err))
    }
}); err != nil {
    return fmt.Errorf("watch config: %w", err)
}
lc.Append(fx.Hook{
    OnStop: func(context.Context) error { return provider.Unwatch() },
})
```

## Getting values

```go
k.String("database.host")
k.Int("server.port")
k.Duration("cache.ttl")
k.Bool("feature.enabled")
k.Strings("allowed_origins")   // []string from comma-sep or YAML list
```

## golusoris usage

- `core/config/` — Koanf instance provided as Fx singleton; environment, YAML,
  and file-watch support.
- Every subpackage config struct tagged with `koanf:"..."`.

## Links

- [Package documentation](https://pkg.go.dev/github.com/knadh/koanf/v2@v2.3.8)
