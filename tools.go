//go:build tools
// +build tools

package capi_webhook

import (
	_ "github.com/daixiang0/gci" // dependency of hack/go-fmt.sh
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
)
