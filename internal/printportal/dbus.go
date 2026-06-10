package printportal

import (
	"context"
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// XDG impl-portal addressing. xdg-desktop-portal routes Print requests to the
// backend that owns this well-known name and object path.
const (
	busName    = "org.freedesktop.impl.portal.desktop.torchio"
	objectPath = "/org/freedesktop/portal/desktop"
	printIface = "org.freedesktop.impl.portal.Print"
)

// dbusBackend adapts the pure Backend to godbus method signatures. The methods
// follow the org.freedesktop.impl.portal.Print interface exactly.
type dbusBackend struct {
	b   *Backend
	ctx context.Context
}

// PreparePrint implements org.freedesktop.impl.portal.Print.PreparePrint.
func (d *dbusBackend) PreparePrint(
	handle dbus.ObjectPath, appID, parentWindow, title string,
	settings, pageSetup, options map[string]dbus.Variant,
) (uint32, map[string]dbus.Variant, *dbus.Error) {
	resp, echo, token := d.b.preparePrint(title, variantsToStrings(settings), variantsToStrings(pageSetup))
	results := map[string]dbus.Variant{
		"settings":   dbus.MakeVariant(stringsToVariants(echo)),
		"page-setup": dbus.MakeVariant(pageSetup),
		"token":      dbus.MakeVariant(token),
	}
	return resp, results, nil
}

// Print implements org.freedesktop.impl.portal.Print.Print. The document
// arrives as a Unix file descriptor.
func (d *dbusBackend) Print(
	handle dbus.ObjectPath, appID, parentWindow, title string,
	fd dbus.UnixFD, options map[string]dbus.Variant,
) (uint32, map[string]dbus.Variant, *dbus.Error) {
	file := os.NewFile(uintptr(fd), "torchio-print")
	if file == nil {
		return responseEnded, nil, dbus.MakeFailedError(fmt.Errorf("printportal: invalid document fd"))
	}
	defer file.Close()

	var token uint32
	if v, ok := options["token"]; ok {
		if t, ok := v.Value().(uint32); ok {
			token = t
		}
	}

	resp, err := d.b.print(d.ctx, title, file, token, variantsToStrings(options))
	if err != nil {
		return resp, nil, dbus.MakeFailedError(err)
	}
	return resp, map[string]dbus.Variant{}, nil
}

// Serve exports the print portal backend on the given connection and requests
// its well-known name. It returns an error if the name is already owned. The
// caller (the daemon) provides a session-bus connection.
func Serve(ctx context.Context, conn *dbus.Conn, b *Backend) error {
	d := &dbusBackend{b: b, ctx: ctx}
	if err := conn.Export(d, objectPath, printIface); err != nil {
		return fmt.Errorf("printportal: export: %w", err)
	}

	// The impl.portal.Print interface advertises a version property.
	propSpec := map[string]map[string]*prop.Prop{
		printIface: {
			"version": {Value: uint32(1), Writable: false, Emit: prop.EmitConst},
		},
	}
	if _, err := prop.Export(conn, objectPath, propSpec); err != nil {
		return fmt.Errorf("printportal: export props: %w", err)
	}

	node := &introspect.Node{
		Name: objectPath,
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name: printIface,
				Methods: []introspect.Method{
					{
						Name: "PreparePrint",
						Args: []introspect.Arg{
							{Name: "handle", Type: "o", Direction: "in"},
							{Name: "app_id", Type: "s", Direction: "in"},
							{Name: "parent_window", Type: "s", Direction: "in"},
							{Name: "title", Type: "s", Direction: "in"},
							{Name: "settings", Type: "a{sv}", Direction: "in"},
							{Name: "page_setup", Type: "a{sv}", Direction: "in"},
							{Name: "options", Type: "a{sv}", Direction: "in"},
							{Name: "response", Type: "u", Direction: "out"},
							{Name: "results", Type: "a{sv}", Direction: "out"},
						},
					},
					{
						Name: "Print",
						Args: []introspect.Arg{
							{Name: "handle", Type: "o", Direction: "in"},
							{Name: "app_id", Type: "s", Direction: "in"},
							{Name: "parent_window", Type: "s", Direction: "in"},
							{Name: "title", Type: "s", Direction: "in"},
							{Name: "fd", Type: "h", Direction: "in"},
							{Name: "options", Type: "a{sv}", Direction: "in"},
							{Name: "response", Type: "u", Direction: "out"},
							{Name: "results", Type: "a{sv}", Direction: "out"},
						},
					},
				},
				Properties: []introspect.Property{
					{Name: "version", Type: "u", Access: "read"},
				},
			},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(node), objectPath,
		"org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("printportal: export introspection: %w", err)
	}

	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("printportal: request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("printportal: name %s already owned", busName)
	}
	return nil
}

// variantsToStrings flattens an a{sv} dict to string values, since
// GtkPrintSettings carries everything as strings.
func variantsToStrings(in map[string]dbus.Variant) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if s, ok := v.Value().(string); ok {
			out[k] = s
		} else {
			out[k] = fmt.Sprint(v.Value())
		}
	}
	return out
}

// stringsToVariants wraps a string map back into an a{sv} dict.
func stringsToVariants(in map[string]string) map[string]dbus.Variant {
	out := make(map[string]dbus.Variant, len(in))
	for k, v := range in {
		out[k] = dbus.MakeVariant(v)
	}
	return out
}
