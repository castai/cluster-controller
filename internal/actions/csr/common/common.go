package common

import (
	"errors"
	"time"
)

// ErrMalformedCSR indicates that the CSR is malformed.
// This error is returned when the CSR does not meet the expected format or
// structure such as not having a name, request, signer name, username, or usages.
var ErrMalformedCSR = errors.New("malformed certificate request")

// OutdatedDuration is the time duration after which a certificate
// request is considered outdated in which case it will be ignored.
var OutdatedDuration = time.Hour
