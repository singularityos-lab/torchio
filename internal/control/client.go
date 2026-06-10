package control

import (
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
)

// Client is a typed wrapper over the JSON-RPC connection to the daemon.
type Client struct {
	c *rpc.Client
}

// Dial connects to the daemon's control socket.
func Dial(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("control: cannot reach daemon at %s (is `torchio daemon` running?): %w", socketPath, err)
	}
	return &Client{c: jsonrpc.NewClient(conn)}, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.c.Close() }

// AddPrinter adds a printer by URI; the daemon probes its capabilities.
func (c *Client) AddPrinter(args AddPrinterArgs) (PrinterDTO, error) {
	var r PrinterReply
	err := c.c.Call("Service.AddPrinter", args, &r)
	return r.Printer, err
}

// ListPrinters returns all configured printers.
func (c *Client) ListPrinters() ([]PrinterDTO, error) {
	var r PrintersReply
	err := c.c.Call("Service.ListPrinters", Empty{}, &r)
	return r.Printers, err
}

// RemovePrinter deletes a printer by name.
func (c *Client) RemovePrinter(name string) error {
	return c.c.Call("Service.RemovePrinter", NameArgs{Name: name}, &Empty{})
}

// SetDefault marks a printer as the default by name.
func (c *Client) SetDefault(name string) error {
	return c.c.Call("Service.SetDefault", NameArgs{Name: name}, &Empty{})
}

// Submit sends a document to a printer through the spooler and returns the job id.
func (c *Client) Submit(args SubmitArgs) (string, error) {
	var r SubmitReply
	err := c.c.Call("Service.Submit", args, &r)
	return r.JobID, err
}

// ListJobs returns jobs matching the filter.
func (c *Client) ListJobs(args ListJobsArgs) ([]JobDTO, error) {
	var r JobsReply
	err := c.c.Call("Service.ListJobs", args, &r)
	return r.Jobs, err
}

// CancelJob cancels a job by its ULID string.
func (c *Client) CancelJob(id string) error {
	return c.c.Call("Service.CancelJob", IDArgs{ID: id}, &Empty{})
}

// JobHistory returns a job and its event log.
func (c *Client) JobHistory(id string) (HistoryReply, error) {
	var r HistoryReply
	err := c.c.Call("Service.JobHistory", IDArgs{ID: id}, &r)
	return r, err
}

// ListDiscovered returns printers seen on the network but not yet configured.
func (c *Client) ListDiscovered() ([]DeviceDTO, error) {
	var r DiscoveredReply
	err := c.c.Call("Service.ListDiscovered", Empty{}, &r)
	return r.Devices, err
}

// AddDiscovered confirms a discovered device as a printer (portal-gated).
func (c *Client) AddDiscovered(args AddDiscoveredArgs) (PrinterDTO, error) {
	var r PrinterReply
	err := c.c.Call("Service.AddDiscovered", args, &r)
	return r.Printer, err
}
