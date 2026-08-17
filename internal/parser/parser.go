package parser

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// FileParser streams events from a JSONL file, tracking byte offsets so that
// ingestion can resume from where a previous pass finished.
type FileParser struct {
	f      *os.File
	r      *bufio.Reader
	offset int64 // byte offset in the file of the *next* line
	size   int64
}

// Open opens the file and seeks to the given start offset. If start == 0
// the file is read from the beginning.
func Open(path string, start int64) (*FileParser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			f.Close()
			return nil, err
		}
	}
	return &FileParser{
		f:      f,
		r:      bufio.NewReaderSize(f, 1<<20), // 1 MiB
		offset: start,
		size:   st.Size(),
	}, nil
}

func (p *FileParser) Close() error { return p.f.Close() }

// Size returns the file size observed when the parser was opened.
func (p *FileParser) Size() int64 { return p.size }

// Offset returns the byte offset of the next line to be read.
func (p *FileParser) Offset() int64 { return p.offset }

// ErrPartialLine indicates the last line in the file lacks a trailing newline.
// Ingestion should NOT advance its stored offset past this line — the writer
// may still be appending to it.
var ErrPartialLine = errors.New("partial line (no trailing newline)")

// Next reads the next event. Returns io.EOF at clean end of file.
// If the last line lacks a newline, returns (event, ErrPartialLine).
func (p *FileParser) Next() (*Event, error) {
	line, err := p.r.ReadBytes('\n')
	if len(line) == 0 && err != nil {
		return nil, err
	}
	lineStart := p.offset
	p.offset += int64(len(line))

	// Strip trailing newline for parsing.
	trimmed := line
	if n := len(trimmed); n > 0 && trimmed[n-1] == '\n' {
		trimmed = trimmed[:n-1]
	}
	if len(trimmed) == 0 {
		// Blank line — skip.
		if err != nil {
			return nil, err
		}
		return p.Next()
	}

	ev := &Event{Offset: lineStart, Raw: append(json.RawMessage(nil), trimmed...)}
	if uerr := json.Unmarshal(trimmed, ev); uerr != nil {
		// Malformed line — surface as an error but let caller decide to skip.
		return ev, uerr
	}
	if err == io.EOF {
		// last line without trailing newline
		return ev, ErrPartialLine
	}
	return ev, nil
}
