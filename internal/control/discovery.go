package control

import (
	"fmt"
	"time"

	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/portal"
)

// ListDiscovered returns the read-only cache of printers seen on the network.
// These are NOT configured printers; they are candidates a user may confirm.
func (s *Service) ListDiscovered(_ Empty, reply *DiscoveredReply) error {
	devices, err := s.store.ListDevices(time.Time{})
	if err != nil {
		return err
	}
	for _, d := range devices {
		reply.Devices = append(reply.Devices, DeviceDTO{
			Key:        d.Key,
			Name:       d.Name,
			URI:        d.URI,
			LastSeenMs: toMs(d.LastSeen),
		})
	}
	return nil
}

// AddDiscovered turns a discovered device into a configured printer, but only
// after the user approves through the ush portal. This is the one place where
// discovery can lead to a configuration change, and it cannot happen without an
// explicit human decision: a network packet alone never creates a printer. The
// resulting printer is tagged OriginDiscoveredConfirmed.
func (s *Service) AddDiscovered(args AddDiscoveredArgs, reply *PrinterReply) error {
	if args.Key == "" {
		return fmt.Errorf("device key is required")
	}

	// Find the device in the read-only cache.
	devices, err := s.store.ListDevices(time.Time{})
	if err != nil {
		return err
	}
	var dev *deviceMatch
	for i := range devices {
		if devices[i].Key == args.Key {
			dev = &deviceMatch{name: devices[i].Name, uri: devices[i].URI}
			break
		}
	}
	if dev == nil {
		return fmt.Errorf("device %q is not in the discovery cache", args.Key)
	}

	name := args.Name
	if name == "" {
		name = dev.name
	}

	// Ask the user, through the portal, before doing anything that changes the
	// configuration. The resource shown is the printer URI (network scope).
	ctx, cancel := s.ctx()
	defer cancel()
	reason := fmt.Sprintf("Add the printer %q found on the network?", dev.name)
	decision, err := s.portal.RequestPermission(ctx, portal.CategoryNetwork, dev.uri, reason)
	if err != nil {
		return fmt.Errorf("portal: %w", err)
	}
	if !decision.Granted() {
		return fmt.Errorf("adding %q was not approved", dev.name)
	}

	p, err := s.admin.Add(ctx, name, dev.uri, model.OriginDiscoveredConfirmed, args.MakeDefault)
	if err != nil {
		return err
	}
	reply.Printer = toPrinterDTO(p)
	return nil
}

// deviceMatch is a tiny holder to avoid importing the store's SeenDevice shape
// into this control flow.
type deviceMatch struct {
	name string
	uri  string
}
