module github.com/golusoris/golusoris/media/audio

go 1.27.1

require (
	github.com/exaring/ebur128 v0.0.0-20260217210235-a476130e2d41
	github.com/go-audio/aiff v1.1.0
	github.com/go-audio/audio v1.0.0
	github.com/go-audio/wav v1.1.0
	github.com/golusoris/golusoris/core v0.9.0
	github.com/hajimehoshi/go-mp3 v0.3.4
	github.com/jfreymuth/oggvorbis v1.0.5
	github.com/mewkiz/flac v1.0.14
	go.uber.org/fx v1.24.0
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/go-audio/riff v1.0.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/icza/bitio v1.1.0 // indirect
	github.com/jfreymuth/vorbis v1.0.2 // indirect
	github.com/knadh/koanf/maps v0.1.3 // indirect
	github.com/knadh/koanf/parsers/json v1.0.1 // indirect
	github.com/knadh/koanf/parsers/yaml v1.1.1 // indirect
	github.com/knadh/koanf/providers/env/v2 v2.0.1 // indirect
	github.com/knadh/koanf/providers/file v1.2.1 // indirect
	github.com/knadh/koanf/v2 v2.3.6 // indirect
	github.com/mewkiz/pkg v0.0.0-20260703220044-4fb89b18cc87 // indirect
	github.com/mewpkg/term v0.0.0-20241026122259-37a80af23985 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/golusoris/golusoris => ../..

replace github.com/golusoris/golusoris/core => ../../core
