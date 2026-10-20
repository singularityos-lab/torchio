// Package control is the daemon's control plane: net/rpc with the JSON codec
// over a Unix domain socket, driven by the CLI. The DTOs here are the wire
// shapes; they avoid leaking model types (model.ID is a byte array that
// JSON would render as a number array, so IDs cross the wire as their ULID
// strings).
package control

// PrinterDTO is the wire form of a printer.
type PrinterDTO struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	URI          string   `json:"uri"`
	Origin       string   `json:"origin"`
	Default      bool     `json:"default"`
	MakeAndModel string   `json:"make_and_model"`
	Formats      []string `json:"formats"`
	Media        []string `json:"media"`
	Sides        []string `json:"sides"`
	Resolutions  []string `json:"resolutions"`
	ColorModes   []string `json:"color_modes"`
	State        int      `json:"state"`
}

// JobDTO is the wire form of a job.
type JobDTO struct {
	ID          string `json:"id"`
	PrinterID   string `json:"printer_id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Format      string `json:"format"`
	Size        int64  `json:"size"`
	RemoteJobID int    `json:"remote_job_id"`
	Error       string `json:"error"`
	CreatedMs   int64  `json:"created_ms"`
	UpdatedMs   int64  `json:"updated_ms"`
}

// EventDTO is the wire form of a job history entry.
type EventDTO struct {
	Seq   int64  `json:"seq"`
	AtMs  int64  `json:"at_ms"`
	Kind  string `json:"kind"`
	From  string `json:"from"`
	To    string `json:"to"`
	Actor string `json:"actor"`
	Cause string `json:"cause"`
}

// TicketDTO mirrors model.JobTicket on the wire.
type TicketDTO struct {
	Copies int    `json:"copies"`
	Sides  string `json:"sides"`
	Media  string `json:"media"`
	Pages  string `json:"pages"`
	Color  string `json:"color"`
}

// --- request/reply argument types (one per RPC method) ---

// AddPrinterArgs adds a printer by URI; the daemon probes its capabilities.
type AddPrinterArgs struct {
	Name        string `json:"name"`
	URI         string `json:"uri"`
	MakeDefault bool   `json:"make_default"`
}

// PrinterReply carries a single printer.
type PrinterReply struct {
	Printer PrinterDTO `json:"printer"`
}

// PrintersReply carries a list of printers.
type PrintersReply struct {
	Printers []PrinterDTO `json:"printers"`
}

// IDArgs references something by its ULID string.
type IDArgs struct {
	ID string `json:"id"`
}

// NameArgs references a printer by name (the CLI's friendly handle).
type NameArgs struct {
	Name string `json:"name"`
}

// Empty is a placeholder for methods with no result.
type Empty struct{}

// SubmitArgs sends a document to a printer through the spooler. The document
// bytes travel inline (base64 in JSON); streaming is a later optimisation.
type SubmitArgs struct {
	Printer string    `json:"printer"` // printer name, or empty for the default
	Name    string    `json:"name"`    // job name
	Ticket  TicketDTO `json:"ticket"`
	Doc     []byte    `json:"doc"`
}

// SubmitReply carries the created job id.
type SubmitReply struct {
	JobID string `json:"job_id"`
}

// ListJobsArgs filters the job list.
type ListJobsArgs struct {
	Printer string `json:"printer"` // printer name filter, optional
	Active  bool   `json:"active"`  // only non-terminal jobs
}

// JobsReply carries a list of jobs.
type JobsReply struct {
	Jobs []JobDTO `json:"jobs"`
}

// HistoryReply carries a job's event log.
type HistoryReply struct {
	Job    JobDTO     `json:"job"`
	Events []EventDTO `json:"events"`
}

// DeviceDTO is a printer found on the network but not (yet) configured. It
// lives in the read-only discovery cache until a user confirms it.
type DeviceDTO struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	URI        string `json:"uri"`
	LastSeenMs int64  `json:"last_seen_ms"`
}

// DiscoveredReply carries the discovered-device cache.
type DiscoveredReply struct {
	Devices []DeviceDTO `json:"devices"`
}

// AddDiscoveredArgs confirms and adds a discovered device as a real printer.
// The daemon asks the portal before creating anything.
type AddDiscoveredArgs struct {
	Key         string `json:"key"`  // devices_seen key
	Name        string `json:"name"` // chosen printer name (defaults to the device name)
	MakeDefault bool   `json:"make_default"`
}
