package client

//go:generate go install github.com/deepmap/oapi-codegen/cmd/oapi-codegen@v1.11.0
//go:generate oapi-codegen -o api.gen.go --old-config-style -generate types -include-tags $API_TAGS -package client $SWAGGER_LOCATION
//go:generate oapi-codegen -o client.gen.go --old-config-style -templates codegen/templates -generate client -include-tags $API_TAGS -package client $SWAGGER_LOCATION
