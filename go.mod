module github.com/wavetermdev/waveterm

go 1.27.3

require (
	github.com/Microsoft/go-winio v0.6.4
	github.com/alexflint/go-filemutex v1.3.0
	github.com/creack/pty v1.1.24
	github.com/ebitengine/purego v0.11.2
	github.com/emirpasic/gods v1.18.1
	github.com/fsnotify/fsnotify v1.10.1
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/golang-migrate/migrate/v4 v4.20.1
	github.com/google/generative-ai-go v0.20.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/gorilla/websocket v1.5.3
	github.com/jmoiron/sqlx v1.4.0
	github.com/joho/godotenv v1.5.1
	github.com/junegunn/fzf v0.74.5
	github.com/kevinburke/ssh_config v1.2.0
	github.com/klauspost/compress v1.20.3
	github.com/launchdarkly/eventsource v1.14.2
	github.com/mattn/go-sqlite3 v1.14.52
	github.com/mitchellh/mapstructure v1.5.0
	github.com/sawka/txwrap v0.2.0
	github.com/shirou/gopsutil/v4 v4.26.9
	github.com/skeema/knownhosts v1.3.3
	github.com/skratchdot/open-golang v0.0.0-20200116055534-eef842397966
	github.com/spf13/cobra v1.10.2
	github.com/ubuntu/gowsl v0.0.0-20240906163211-049fd49bd93b
	github.com/wavetermdev/htmltoken v0.2.0
	github.com/wavetermdev/waveterm/tsunami v0.12.3
	golang.org/x/crypto v0.58.0
	golang.org/x/mod v0.42.0
	golang.org/x/sync v0.24.0
	golang.org/x/sys v0.49.0
	golang.org/x/term v0.47.0
	google.golang.org/api v0.310.0
)

require (
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/ai v0.8.0 // indirect
	cloud.google.com/go/auth v0.24.1-0.20261001053825-dbc26066f70a // indirect
	cloud.google.com/go/auth/oauth2adapt v0.3.0 // indirect
	cloud.google.com/go/compute/metadata v0.10.0 // indirect
	cloud.google.com/go/longrunning v1.1.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/google/s2a-go v0.1.11 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.24 // indirect
	github.com/googleapis/gax-go/v2 v2.27.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/junegunn/go-shellwords v0.0.0-20250127100254-2aa3b3277741 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/power-devops/perfstat v0.0.0-20260805114148-88456608a4f6 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/ubuntu/decorate v0.0.0-20230125165522-2d5b0a9bb117 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.69.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20261012220534-4341edf3f941 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261019180522-ce908d29299b // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/kevinburke/ssh_config => github.com/wavetermdev/ssh_config v0.0.0-20241219203747-6409e4292f34

replace github.com/creack/pty => github.com/photostorm/pty v1.1.19-0.20230903182454-31354506054b

replace github.com/wavetermdev/waveterm/tsunami => ./tsunami
