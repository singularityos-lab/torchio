package control

import (
	"time"

	"github.com/singularityos-lab/torchio/internal/model"
)

// toPrinterDTO converts a model.Printer to its wire form.
func toPrinterDTO(p *model.Printer) PrinterDTO {
	d := PrinterDTO{
		ID:           p.ID.String(),
		Name:         p.Name,
		URI:          p.URI,
		Origin:       string(p.Origin),
		Default:      p.Default,
		MakeAndModel: p.Caps.MakeAndModel,
		Media:        p.Caps.Media,
		Sides:        p.Caps.Sides,
		Resolutions:  p.Caps.Resolutions,
		ColorModes:   p.Caps.ColorModes,
		State:        p.Caps.State,
	}
	for _, f := range p.Caps.DocumentFormats {
		d.Formats = append(d.Formats, f.String())
	}
	return d
}

// toJobDTO converts a model.Job to its wire form.
func toJobDTO(j *model.Job) JobDTO {
	return JobDTO{
		ID:          j.ID.String(),
		PrinterID:   j.PrinterID.String(),
		Name:        j.Name,
		State:       j.State,
		Format:      j.Document.Format.String(),
		Size:        j.Document.Size,
		RemoteJobID: j.RemoteJobID,
		Error:       j.Error,
		CreatedMs:   toMs(j.Created),
		UpdatedMs:   toMs(j.Updated),
	}
}

// toEventDTO converts a model.Event to its wire form.
func toEventDTO(e *model.Event) EventDTO {
	return EventDTO{
		Seq:   e.Seq,
		AtMs:  toMs(e.At),
		Kind:  string(e.Kind),
		From:  string(e.From),
		To:    string(e.To),
		Actor: e.Actor,
		Cause: e.Cause,
	}
}

func toMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
