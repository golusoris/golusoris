# knadh/koanf/v2 — v2.3.4 snapshot

Pinned: **v2.3.4**
Source: https://pkg.go.dev/github.com/knadh/koanf/v2@v2.3.4

## Loading config

```go
k := koanf.New(".")

// Load from environment (prefix APP_, delimiter __)
k.Load(env.Provider("APP_", ".", func(s string) string {
    return strings.ReplaceAll(strings.ToLower(
        strings.TrimPrefix(s, "APP_")), "__", ".")
}), nil)

// Load from YAML file
k.Load(file.Provider("config.yaml"), yaml.Parser())

// Load from embedded FS
k.Load(fs.Provider(embeddedFS, "config.yaml"), yaml.Parser())
```

## Unmarshalling

```go
var cfg MyConfig
if err := k.Unmarshal("", &cfg); err != nil {
    return err
}

// With custom decode hooks (used in golusoris config/)
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
w, _ := filewatcher.New(filewatcher.Config{
    Path: "config.yaml",
    Callback: func(event fsnotify.Event, err error) {
        if err != nil { return }
        k.Load(file.Provider("config.yaml"), yaml.Parser())
    },
})
_ = w.Start()
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

- `config/` — koanf instance provided as fx singleton; env + YAML + file-watch enabled.
- Every subpackage config struct tagged with `koanf:"..."`.

## Links

- Changelog: https://github.com/knadh/koanf/blob/master/CHANGELOG.md
