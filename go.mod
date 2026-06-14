module github.com/golusoris/golusoris

go 1.26.4

require (
	charm.land/bubbletea/v2 v2.0.7
	filippo.io/csrf v0.2.1
	github.com/99designs/gqlgen v0.17.91
	github.com/CAFxX/httpcompression v0.0.9
	github.com/ClickHouse/clickhouse-go/v2 v2.46.0
	github.com/Khan/genqlient v0.8.1
	github.com/SherClockHolmes/webpush-go v1.4.0
	github.com/alexedwards/argon2id v1.0.0
	github.com/aws/aws-sdk-go-v2 v1.42.0
	github.com/aws/aws-sdk-go-v2/config v1.32.25
	github.com/aws/aws-sdk-go-v2/credentials v1.19.24
	github.com/aws/aws-sdk-go-v2/service/s3 v1.103.3
	github.com/aws/smithy-go v1.27.2
	github.com/benbjohnson/hashfs v0.2.2
	github.com/bmaupin/go-epub v1.1.0
	github.com/brianvoe/gofakeit/v7 v7.15.0
	github.com/caddyserver/certmagic v0.25.3
	github.com/casbin/casbin/v3 v3.10.0
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/cilium/ebpf v0.21.0
	github.com/coder/websocket v1.8.14
	github.com/coreos/go-oidc/v3 v3.18.0
	github.com/emersion/go-smtp v0.24.0
	github.com/fsnotify/fsnotify v1.10.1
	github.com/getkin/kin-openapi v0.140.0
	github.com/getsentry/sentry-go v0.46.2
	github.com/gkampitakis/go-snaps v0.5.22
	github.com/go-chi/chi/v5 v5.3.0
	github.com/go-faster/errors v0.7.1
	github.com/go-playground/form/v4 v4.3.0
	github.com/go-playground/validator/v10 v10.30.3
	github.com/go-viper/mapstructure/v2 v2.5.0
	github.com/go-webauthn/webauthn v0.17.4
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/golang-migrate/migrate/v4 v4.19.1
	github.com/google/uuid v1.6.0
	github.com/grafana/pyroscope-go v1.3.1
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.3.3
	github.com/hashicorp/go-retryablehttp v0.7.8
	github.com/jackc/pglogrepl v0.0.0-20260401131349-e37c41485510
	github.com/jackc/pgx/v5 v5.10.0
	github.com/jonboulle/clockwork v0.5.0
	github.com/knadh/koanf/parsers/json v1.0.0
	github.com/knadh/koanf/parsers/yaml v1.1.0
	github.com/knadh/koanf/providers/env/v2 v2.0.0
	github.com/knadh/koanf/providers/file v1.2.1
	github.com/knadh/koanf/v2 v2.3.5
	github.com/leanovate/gopter v0.2.11
	github.com/lmittmann/tint v1.1.3
	github.com/mattn/go-isatty v0.0.22
	github.com/maypok86/otter/v2 v2.3.0
	github.com/mholt/archives v0.1.5
	github.com/miekg/dns v1.1.72
	github.com/minio/selfupdate v0.6.0
	github.com/modelcontextprotocol/go-sdk v1.6.1
	github.com/nats-io/nats.go v1.52.0
	github.com/nbutton23/zxcvbn-go v0.0.0-20210217022336-fa2cb2858354
	github.com/nguyenthenguyen/docx v0.0.0-20230621112118-9c8e795a11db
	github.com/nicksnyder/go-i18n/v2 v2.6.1
	github.com/ogen-go/ogen v1.20.3
	github.com/oschwald/maxminddb-golang/v2 v2.4.0
	github.com/pgvector/pgvector-go v0.4.0
	github.com/pgvector/pgvector-go/pgx v0.4.0
	github.com/pion/webrtc/v4 v4.2.15
	github.com/pquerna/otp v1.5.0
	github.com/prometheus/client_golang v1.23.2
	github.com/redis/rueidis v1.0.75
	github.com/riverqueue/river v0.39.0
	github.com/riverqueue/river/riverdriver/riverpgxv5 v0.39.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/rs/cors v1.11.1
	github.com/segmentio/ksuid v1.0.4
	github.com/sony/gobreaker/v2 v2.4.0
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.11.1
	github.com/stripe/stripe-go/v86 v86.0.0
	github.com/testcontainers/testcontainers-go v0.42.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.42.0
	github.com/testcontainers/testcontainers-go/modules/redis v0.42.0
	github.com/tsenart/vegeta/v12 v12.13.0
	github.com/twmb/franz-go v1.21.3
	github.com/ulule/limiter/v3 v3.11.2
	github.com/wneessen/go-mail v0.7.3
	github.com/xuri/excelize/v2 v2.10.1
	github.com/yuin/goldmark v1.8.2
	github.com/zeebo/blake3 v0.2.4
	go.opentelemetry.io/contrib/bridges/otelslog v0.19.0
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.69.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0
	go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.20.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0
	go.opentelemetry.io/otel/log v0.20.0
	go.opentelemetry.io/otel/sdk v1.44.0
	go.opentelemetry.io/otel/sdk/log v0.20.0
	go.opentelemetry.io/otel/sdk/metric v1.44.0
	go.opentelemetry.io/otel/trace v1.44.0
	go.temporal.io/sdk v1.44.1
	go.uber.org/fx v1.24.0
	golang.org/x/crypto v0.53.0
	golang.org/x/net v0.56.0
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.21.0
	golang.org/x/text v0.38.0
	google.golang.org/grpc v1.81.1
	k8s.io/client-go v0.36.2
	riverqueue.com/riverui v0.16.0
)

require (
	aead.dev/minisign v0.3.0 // indirect
	dario.cat/mergo v1.0.2 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/ClickHouse/ch-go v0.72.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/STARRY-S/zip v0.2.3 // indirect
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.13 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.30 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.22 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.29 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.29 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.2.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.31.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.36.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.43.3 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/bodgit/plumbing v1.3.0 // indirect
	github.com/bodgit/sevenzip v1.6.4 // indirect
	github.com/bodgit/windows v1.0.1 // indirect
	github.com/boombuler/barcode v1.1.0 // indirect
	github.com/caddyserver/zerossl v0.1.5 // indirect
	github.com/casbin/govaluate v1.10.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260525132238-948f4557a654 // indirect
	github.com/charmbracelet/x/ansi v0.11.7 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/platforms v0.2.1 // indirect
	github.com/cpuguy83/dockercfg v0.3.2 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/docker/go-connections v0.7.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dsnet/compress v0.0.2-0.20230904184137-39efe44ab707 // indirect
	github.com/ebitengine/purego v0.10.1 // indirect
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6 // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/gkampitakis/ciinfo v0.3.4 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/jx v1.2.0 // indirect
	github.com/go-faster/yaml v0.4.6 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.23.1 // indirect
	github.com/go-openapi/jsonreference v0.21.6 // indirect
	github.com/go-openapi/swag v0.26.1 // indirect
	github.com/go-openapi/swag/cmdutils v0.26.1 // indirect
	github.com/go-openapi/swag/conv v0.26.1 // indirect
	github.com/go-openapi/swag/fileutils v0.26.1 // indirect
	github.com/go-openapi/swag/jsonname v0.26.1 // indirect
	github.com/go-openapi/swag/jsonutils v0.26.1 // indirect
	github.com/go-openapi/swag/loading v0.26.1 // indirect
	github.com/go-openapi/swag/mangling v0.26.1 // indirect
	github.com/go-openapi/swag/netutils v0.26.1 // indirect
	github.com/go-openapi/swag/stringutils v0.26.1 // indirect
	github.com/go-openapi/swag/typeutils v0.26.1 // indirect
	github.com/go-openapi/swag/yamlutils v0.26.1 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-webauthn/x v0.2.6 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/gofrs/uuid v4.4.0+incompatible // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/mock v1.6.0 // indirect
	github.com/google/gnostic-models v0.7.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/grafana/pyroscope-go/godeltaprof v0.1.11 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/influxdata/tdigest v0.0.1 // indirect
	github.com/jackc/pgerrcode v0.0.0-20250907135507-afb5586c32a6 // indirect
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/klauspost/pgzip v1.2.6 // indirect
	github.com/knadh/koanf/maps v0.1.2 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/libdns/libdns v1.1.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20260330125221-c963978e514e // indirect
	github.com/magiconair/properties v1.8.10 // indirect
	github.com/mailru/easyjson v0.9.2 // indirect
	github.com/maruel/natural v1.3.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/mdelapenya/tlscert v0.2.0 // indirect
	github.com/mholt/acmez/v3 v3.1.6 // indirect
	github.com/mikelolasagasti/xz v1.0.1 // indirect
	github.com/minio/minlz v1.1.1 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/go-archive v0.2.0 // indirect
	github.com/moby/moby/api v1.54.2 // indirect
	github.com/moby/moby/client v0.4.1 // indirect
	github.com/moby/patternmatcher v0.6.1 // indirect
	github.com/moby/sys/sequential v0.7.0 // indirect
	github.com/moby/sys/user v0.4.0 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/nexus-rpc/nexus-proto-annotations v0.1.0 // indirect
	github.com/nexus-rpc/sdk-go v0.6.0 // indirect
	github.com/nwaples/rardecode/v2 v2.2.5 // indirect
	github.com/oasdiff/yaml v0.1.0 // indirect
	github.com/oasdiff/yaml3 v0.0.13 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/paulmach/orb v0.13.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/pion/datachannel v1.6.0 // indirect
	github.com/pion/dtls/v3 v3.1.4 // indirect
	github.com/pion/ice/v4 v4.2.7 // indirect
	github.com/pion/interceptor v0.1.45 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/mdns/v2 v2.1.0 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.16 // indirect
	github.com/pion/rtp v1.10.2 // indirect
	github.com/pion/sctp v1.10.0 // indirect
	github.com/pion/sdp/v3 v3.0.18 // indirect
	github.com/pion/srtp/v3 v3.0.12 // indirect
	github.com/pion/stun/v3 v3.1.5 // indirect
	github.com/pion/transport/v4 v4.0.2 // indirect
	github.com/pion/turn/v5 v5.0.10 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.68.1 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/richardlehane/mscfb v1.0.7 // indirect
	github.com/richardlehane/msoleps v1.0.6 // indirect
	github.com/riverqueue/apiframe v0.0.0-20260609005548-64f48e0e6228 // indirect
	github.com/riverqueue/river/riverdriver v0.39.0 // indirect
	github.com/riverqueue/river/rivershared v0.39.0 // indirect
	github.com/riverqueue/river/rivertype v0.39.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/robfig/cron v1.2.0 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/rs/dnscache v0.0.0-20230804202142-fc85eb664529 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sergi/go-diff v1.4.0 // indirect
	github.com/shirou/gopsutil/v4 v4.26.5 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/sorairolake/lzip-go v0.3.8 // indirect
	github.com/sosodev/duration v1.4.0 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/stangelandcl/ppmd v0.1.1 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/tiendc/go-deepcopy v1.7.2 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.13.1 // indirect
	github.com/ulikunitz/xz v0.5.15 // indirect
	github.com/vektah/gqlparser/v2 v2.5.34 // indirect
	github.com/vincent-petithory/dataurl v1.0.0 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	go.temporal.io/api v1.62.14 // indirect
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/goleak v1.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	go.uber.org/zap/exp v0.3.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	go4.org v0.0.0-20260112195520-a5071408f32f // indirect
	golang.org/x/exp v0.0.0-20260611194520-c48552f49976 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/term v0.44.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.46.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260610212136-7ab31c22f7ad // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/api v0.36.2 // indirect
	k8s.io/apimachinery v0.36.2 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-openapi v0.0.0-20260603220949-865597e52e25 // indirect
	k8s.io/utils v0.0.0-20260507154919-ff6756f316d2 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.4.0 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
