//go:build !linux

package cli

import (
	"context"
	"fmt"
)

// runUsb is unavailable off Linux: IPP-over-USB uses Linux usbfs directly.
func runUsb(context.Context, []string) error {
	return fmt.Errorf("torchio usb is only supported on Linux (usbfs)")
}
