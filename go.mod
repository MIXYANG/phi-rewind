module github.com/MIXYANG/phi-rewind

go 1.26.3

require (
	github.com/pulseaiclub/phi/ext/go v0.25.4
	github.com/stretchr/testify v1.12.1
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect

// Local development until the host APIs have a released SDK version.
replace github.com/pulseaiclub/phi/ext/go => ../phi/ext/go
