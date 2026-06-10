// Package detect determines a document's real format from its bytes. Principle:
// the client-supplied Content-Type is only a hint; Torchio trusts the magic
// bytes. A document whose declared type does not match its content is rejected
// before it ever reaches a transformer (the "validating" job state), which is
// the cheapest possible place to stop a malformed or hostile file.
package detect

import (
	"bufio"
	"bytes"
	"io"

	"github.com/singularityos-lab/torchio/internal/model"
)

// sniffLen is how many leading bytes we examine. Enough for every signature
// below, and for a short scan for the PDF header which some files prefix with
// whitespace or a UTF-8 BOM.
const sniffLen = 1024

// Format inspects up to the first sniffLen bytes of r and returns the detected
// MIME together with a reader that replays the full original stream (the
// sniffed prefix is buffered and re-emitted, so r is not consumed for the
// caller). If nothing matches, it returns MIMEOctet.
func Format(r io.Reader) (model.MIME, io.Reader, error) {
	br := bufio.NewReaderSize(r, sniffLen)
	head, err := br.Peek(sniffLen)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return model.MIMEOctet, br, err
	}
	return FormatBytes(head), br, nil
}

// FormatBytes detects the format from an in-memory prefix. Exposed separately
// so the spooler can sniff a buffer it already holds.
func FormatBytes(head []byte) model.MIME {
	switch {
	case hasPrefixWithin(head, []byte("%PDF-"), 32):
		return model.MIMEPDF
	case bytes.HasPrefix(head, []byte("%!PS")), bytes.HasPrefix(head, []byte{0xc5, 0xd0, 0xd3, 0xc6}):
		// "%!PS" or the DOS EPS binary header.
		return model.MIMEPostScript
	case bytes.HasPrefix(head, []byte{0xff, 0xd8, 0xff}):
		return model.MIMEJPEG
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return model.MIMEPNG
	case bytes.HasPrefix(head, []byte("RaS2")), bytes.HasPrefix(head, []byte("UNIRAST")):
		// PWG Raster ("RaS2"/"RaS3") and Apple URF ("UNIRAST").
		return pwgOrURF(head)
	case len(head) > 0 && isProbablyText(head):
		return model.MIMEText
	default:
		return model.MIMEOctet
	}
}

func pwgOrURF(head []byte) model.MIME {
	if bytes.HasPrefix(head, []byte("UNIRAST")) {
		return model.MIMEURF
	}
	return model.MIMEPWGRaster
}

// hasPrefixWithin reports whether sig appears at or near the start of b, within
// the first window bytes. PDFs are allowed a little leading junk by the spec.
func hasPrefixWithin(b, sig []byte, window int) bool {
	if window > len(b) {
		window = len(b)
	}
	idx := bytes.Index(b[:window], sig)
	return idx >= 0
}

// isProbablyText reports whether the prefix looks like UTF-8/ASCII text: no NUL
// bytes and only a small fraction of non-printable, non-whitespace bytes.
func isProbablyText(b []byte) bool {
	if bytes.IndexByte(b, 0x00) >= 0 {
		return false
	}
	var suspicious int
	for _, c := range b {
		if c < 0x09 || (c > 0x0d && c < 0x20) {
			suspicious++
		}
	}
	return suspicious*100/len(b) < 5
}
