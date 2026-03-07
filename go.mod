module github.com/hazyhaar/pkg

go 1.25.0

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/modelcontextprotocol/go-sdk v1.3.1
	github.com/quic-go/quic-go v0.59.0
	go.uber.org/goleak v1.3.0
	golang.org/x/net v0.51.0
	golang.org/x/oauth2 v0.35.0
	golang.org/x/text v0.34.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.46.1
	github.com/hazyhaar/pdfast v0.1.0
)

require (
	cloud.google.com/go/compute/metadata v0.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.3 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/exp v0.0.0-20251023183803-a4bb9ffd2546 // indirect
	golang.org/x/image v0.36.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	modernc.org/libc v1.67.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/hazyhaar/pdfast => ../pdfast
