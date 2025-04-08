package wrapper

import "errors"

var (
	// ErrMalformedCSR indicates that the CSR is malformed.
	// This error is returned when the CSR does not meet the expected format or
	// structure such as not having a name, request, signer name, username, or usages.
	ErrMalformedCSR = errors.New("malformed certificate request")

	// ErrAlreadyDenied indicates that the CSR already has approved condition.
	ErrAlreadyApproved = errors.New("certificate signing request already approved")
)
