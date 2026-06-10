// Package model holds Torchio's domain types: printers, their capabilities,
// jobs and the documents that flow through them. It is deliberately free of
// transport and storage concerns so it can be shared by the IPP client, the
// spooler and the CLI without import cycles.
package model

// MIME is a document format identifier, e.g. "application/pdf". Torchio keys
// the whole transform graph on these, so a small named type keeps intent clear
// at call sites.
type MIME string

// The MIME types Torchio reasons about directly. Printers advertise these in
// "document-format-supported"; the transform graph uses application/pdf and
// image/pwg-raster as its two internal pivots.
const (
	MIMEPDF        MIME = "application/pdf"
	MIMEPostScript MIME = "application/postscript"
	MIMEPWGRaster  MIME = "image/pwg-raster"
	MIMEURF        MIME = "image/urf" // Apple Raster (AirPrint)
	MIMEPCLm       MIME = "application/PCLm"
	MIMEJPEG       MIME = "image/jpeg"
	MIMEPNG        MIME = "image/png"
	MIMEText       MIME = "text/plain"
	MIMEOctet      MIME = "application/octet-stream"
)

// String returns the MIME type as a plain string.
func (m MIME) String() string { return string(m) }
