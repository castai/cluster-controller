package actions

import "fmt"

func newUnexpectedTypeErr(value interface{}, expectedType interface{}) error {
	return fmt.Errorf("unexpected type %T, expected %T", value, expectedType)
}
