package client

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -o api.gen.go --old-config-style -generate types -include-tags $API_TAGS -package client $SWAGGER_LOCATION
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -o client.gen.go --old-config-style -templates codegen/templates -generate client -include-tags $API_TAGS -package client $SWAGGER_LOCATION
