module office-craft-api

go 1.22

require (
	github.com/gofiber/fiber/v2 v2.52.5
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/golang-migrate/migrate/v4 v4.17.1
	github.com/jackc/pgx/v5 v5.6.0
	github.com/joho/godotenv v1.5.1
)

require (
	github.com/andybalholm/brotli v1.0.5 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/puddle/v2 v2.2.1 // indirect
	github.com/klauspost/compress v1.17.0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/stretchr/testify v1.9.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.51.0 // indirect
	github.com/valyala/tcplisten v1.0.0 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	golang.org/x/crypto v0.20.0 // indirect
	golang.org/x/sync v0.5.0 // indirect
	golang.org/x/sys v0.17.0 // indirect
	golang.org/x/text v0.14.0 // indirect
)

// NOTE: this sandbox environment cannot reach golang.org (only github.com is
// allow-listed on its egress proxy), so golang.org/x/* modules are mirrored
// from their canonical GitHub read-only mirrors here purely to let `go build`
// succeed while iterating in this container. This block is safe to delete
// once you run `go mod tidy` on a machine with normal internet access - Go
// will simply refetch the real golang.org/x/* modules from proxy.golang.org.
replace (
	go.uber.org/atomic => github.com/uber-go/atomic v1.7.0
	golang.org/x/crypto => github.com/golang/crypto v0.20.0
	golang.org/x/net => github.com/golang/net v0.21.0
	golang.org/x/sync => github.com/golang/sync v0.5.0
	golang.org/x/sys => github.com/golang/sys v0.17.0
	golang.org/x/text => github.com/golang/text v0.14.0
	golang.org/x/tools => github.com/golang/tools v0.10.0
	gopkg.in/check.v1 => github.com/go-check/check v0.0.0-20200902074654-038fdea0a05b
	gopkg.in/yaml.v3 => github.com/go-yaml/yaml v0.0.0-20220521103104-8f96da9f5d5e
)
