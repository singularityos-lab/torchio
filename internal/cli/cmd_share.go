package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/singularityos-lab/torchio/internal/control"
	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/share"
)

// runShare implements `torchio share <printer>`: it re-exposes a local printer
// to the network over IPPS and announces it via DNS-SD, forwarding incoming
// jobs to the spooler. This is the only component that opens a network port.
func runShare(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	listen := fs.String("listen", ":0", "address to expose the shared printer on (all interfaces by default)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: torchio share <printer-name>")
	}
	name := fs.Arg(0)

	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()

	printers, err := client.ListPrinters()
	if err != nil {
		return err
	}
	var dto *control.PrinterDTO
	for i := range printers {
		if printers[i].Name == name {
			dto = &printers[i]
			break
		}
	}
	if dto == nil {
		return fmt.Errorf("no printer named %q (see `torchio printer ls`)", name)
	}

	caps, primary := capsFromDTO(*dto)

	// Forward each received job to the spooler.
	submit := func(_ context.Context, _ string, doc io.Reader) error {
		data, err := io.ReadAll(doc)
		if err != nil {
			return err
		}
		_, err = client.Submit(control.SubmitArgs{Printer: name, Name: "shared job", Doc: data})
		return err
	}

	srv := share.New(name, caps, primary, submit)
	cert, err := share.SelfSignedCert()
	if err != nil {
		return err
	}
	if err := srv.ListenTLS(*listen, cert); err != nil {
		return err
	}
	fmt.Printf("sharing %q at %s\n", name, srv.URI())

	// Announce on the network (best effort: multicast may be unavailable).
	go runResponder(ctx, name, srv.Addr())

	fmt.Println("Ctrl-C to stop sharing")
	return srv.Serve(ctx)
}

// runResponder starts a DNS-SD responder announcing the shared printer.
func runResponder(ctx context.Context, name, addr string) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}
	p := 0
	fmt.Sscanf(port, "%d", &p)

	host, _ := os.Hostname()
	if host == "" {
		host = "torchio"
	}
	svc := share.ServiceInfo{
		Instance: name,
		Type:     "_ipps._tcp.local.",
		Host:     host + ".local.",
		Port:     p,
		TXT: map[string]string{
			"rp":      "ipp/print",
			"ty":      name,
			"pdl":     "application/pdf,image/pwg-raster,image/urf",
			"txtvers": "1",
		},
		Addrs: localIPv4(),
	}
	r := share.NewResponder(svc)
	if err := r.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "discovery announce disabled: %v\n", err)
	}
}

// capsFromDTO rebuilds the typed capabilities from the control-plane DTO.
func capsFromDTO(dto control.PrinterDTO) (model.Capabilities, model.MIME) {
	caps := model.Capabilities{
		MakeAndModel: dto.MakeAndModel,
		State:        dto.State,
		Media:        dto.Media,
		Sides:        dto.Sides,
		Resolutions:  dto.Resolutions,
		ColorModes:   dto.ColorModes,
	}
	for _, f := range dto.Formats {
		caps.DocumentFormats = append(caps.DocumentFormats, model.MIME(f))
	}
	primary := model.MIMEPDF
	if len(caps.DocumentFormats) > 0 {
		primary = caps.DocumentFormats[0]
	}
	return caps, primary
}

// localIPv4 returns the non-loopback IPv4 addresses of this host, for the
// DNS-SD announcement.
func localIPv4() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if v4 := ipnet.IP.To4(); v4 != nil {
				out = append(out, v4)
			}
		}
	}
	return out
}
