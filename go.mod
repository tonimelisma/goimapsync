module github.com/tonimelisma/pimvault

go 1.27.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/emersion/go-ical v0.0.0-20250609112844-439c63cef608
	github.com/emersion/go-imap/v2 v2.0.0-beta.9
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6
	github.com/emersion/go-vcard v0.1.0
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.12.1
	golang.org/x/oauth2 v0.37.0
)

require (
	github.com/emersion/go-message v0.18.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/teambition/rrule-go v1.8.2 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

// OnTokenChange hook; see docs/requirements.md D134.
replace golang.org/x/oauth2 => github.com/tonimelisma/oauth2 v0.0.0-20260209071456-249baba404ab
