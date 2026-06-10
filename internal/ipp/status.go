package ipp

import (
	"errors"
	"fmt"

	"github.com/OpenPrinting/goipp"
)

// StatusError is returned when a printer replies with a non-success IPP status
// code. It carries the structured status so callers can branch on it (for
// example, the spooler treats not-accepting-jobs differently from bad-request).
type StatusError struct {
	Status goipp.Status
	Detail string // optional status-message text from the printer
}

func (e *StatusError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("printer returned %s: %s", e.Status, e.Detail)
	}
	return fmt.Sprintf("printer returned %s", e.Status)
}

// statusOK reports whether a status code is one of the IPP "successful-*"
// family (0x0000..0x00ff).
func statusOK(s goipp.Status) bool {
	return s >= goipp.StatusOk && s < 0x0100
}

// statusMessage extracts the printer's human-readable "status-message" from a
// response, if it included one.
func statusMessage(m *goipp.Message) string {
	if s, ok := FirstString(m.Operation, "status-message"); ok {
		return s
	}
	return ""
}

// AsStatusError unwraps err into a *StatusError if it is one.
func AsStatusError(err error) (*StatusError, bool) {
	var se *StatusError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}
