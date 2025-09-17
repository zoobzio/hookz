module misbehaving-test

go 1.23

toolchain go1.24.5

replace github.com/zoobzio/hookz => ../../

require (
	github.com/stretchr/testify v1.11.1
	github.com/zoobzio/hookz v0.0.0-00010101000000-000000000000
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
