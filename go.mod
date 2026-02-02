module lns

go 1.24.0

require (
	github.com/fatih/color v1.18.0
	github.com/spf13/cobra v1.9.1
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	golang.org/x/sys v0.39.0 // indirect
)

replace github.com/cpuguy83/go-md2man/v2 => ./internal/third_party/go-md2man

replace github.com/inconshreveable/mousetrap => ./internal/third_party/mousetrap

replace gopkg.in/check.v1 => ./internal/third_party/check
