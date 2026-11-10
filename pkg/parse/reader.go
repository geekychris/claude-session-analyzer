package parse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// ParseLine decodes one JSONL line (without or with its trailing newline).
// A blank line returns (Record{}, ErrBlankLine). The returned Record's Raw
// holds a copy of the trimmed line.
func ParseLine(line []byte) (Record, error) {
	trimmed := bytes.TrimRight(line, "\r\n")
	if len(bytes.TrimSpace(trimmed)) == 0 {
		return Record{}, ErrBlankLine
	}
	rec := Record{Raw: append(json.RawMessage(nil), trimmed...)}
	if err := json.Unmarshal(trimmed, &rec); err != nil {
		return rec, err
	}
	return rec, nil
}

// ErrBlankLine is returned by ParseLine for an empty line.
var ErrBlankLine = errors.New("blank line")

// Stats describes what Records / Reader saw besides the records themselves.
type Stats struct {
	Lines     int   // non-blank lines seen
	Malformed int   // lines that were not valid JSON (skipped)
	Partial   bool  // the input ended without a trailing newline
	Bytes     int64 // bytes consumed
}

// Records decodes every record in r. It is tolerant: malformed lines are
// skipped, unknown record types are returned like any other (Type set, the
// typed fields zero), and a final line without a newline is included if it
// parses. The error is non-nil only when reading r fails.
func Records(r io.Reader) ([]Record, error) {
	recs, _, err := RecordsWithStats(r)
	return recs, err
}

// RecordsWithStats is Records plus counts of skipped and partial lines.
func RecordsWithStats(r io.Reader) ([]Record, Stats, error) {
	rd := NewReader(r, 0)
	var out []Record
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil && !errors.Is(err, ErrPartialLine) {
			if rec != nil {
				continue // malformed line (counted in Stats); skip it
			}
			return out, rd.Stats(), err
		}
		out = append(out, *rec)
		if errors.Is(err, ErrPartialLine) {
			break
		}
	}
	return out, rd.Stats(), nil
}

// Reader streams records from any io.Reader, tracking absolute byte offsets
// (base + bytes consumed) so callers can resume.
type Reader struct {
	r      *bufio.Reader
	offset int64
	stats  Stats
}

// NewReader reads from r; base is the absolute offset of r's first byte.
func NewReader(r io.Reader, base int64) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 1<<20), offset: base}
}

// Offset is the absolute offset of the next unread byte.
func (p *Reader) Offset() int64 { return p.offset }

// Stats returns counts so far.
func (p *Reader) Stats() Stats { return p.stats }

// Next returns the next record. io.EOF at a clean end; (rec, ErrPartialLine)
// for a final line without a newline that parsed; a JSON error (with a
// non-nil Record carrying Offset and Raw) for a malformed line — the bytes are
// consumed, so calling Next again moves on.
func (p *Reader) Next() (*Record, error) {
	for {
		line, err := p.r.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			return nil, err
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		start := p.offset
		p.offset += int64(len(line))
		p.stats.Bytes += int64(len(line))
		partial := errors.Is(err, io.EOF)
		if partial {
			p.stats.Partial = true
		}
		rec, perr := ParseLine(line)
		if errors.Is(perr, ErrBlankLine) {
			if partial {
				return nil, io.EOF
			}
			continue
		}
		p.stats.Lines++
		rec.Offset = start
		var te *json.UnmarshalTypeError
		if errors.As(perr, &te) {
			// A field had an unexpected shape; the rest decoded. Keep it.
			perr = nil
		}
		if perr != nil {
			p.stats.Malformed++
			return &rec, perr
		}
		if partial {
			return &rec, ErrPartialLine
		}
		return &rec, nil
	}
}

// Message is a Record flattened into what the analyzers need.
type Message struct {
	// Role is user, assistant, system or meta (header/bookkeeping records).
	Role string
	// Subtype: "tool_result" for a user record that only carries tool
	// results (not a human turn); the system subtype for system records; the
	// record type for meta records.
	Subtype string
	// Text is the human-readable text: user/assistant text blocks, tool
	// result text (for user tool_result carriers), "[tool_use Name]" markers
	// for assistant tool calls. Thinking is excluded.
	Text string
	// AssistantText is only the assistant's own text blocks (no markers).
	AssistantText string
	Model         string
	Usage         Usage
	// MessageID is the API message id (assistant); several records can share
	// one when Claude Code splits a response into one line per block.
	MessageID   string
	ToolUses    []ContentBlock
	ToolResults []ContentBlock
}

// Normalize flattens a record. It never fails: content it cannot decode is
// treated as empty.
func Normalize(rec Record) Message {
	var m Message
	switch rec.Type {
	case "user":
		m.Role = "user"
		if rec.Message == nil {
			break
		}
		blocks, isList, _ := DecodeContentBlocks(rec.Message.Content)
		if !isList {
			m.Text = StringContent(rec.Message.Content)
			break
		}
		var sb strings.Builder
		sawText, sawResult := false, false
		for _, b := range blocks {
			switch b.Type {
			case "text":
				sb.WriteString(b.Text)
				sb.WriteString("\n")
				sawText = true
			case "tool_result":
				m.ToolResults = append(m.ToolResults, b)
				sb.WriteString(StringifyResult(b.ResultContent))
				sb.WriteString("\n")
				sawResult = true
			}
		}
		m.Text = strings.TrimSpace(sb.String())
		if sawResult && !sawText {
			m.Subtype = "tool_result"
		}
	case "assistant":
		m.Role = "assistant"
		if rec.Message == nil {
			break
		}
		m.Model = rec.Message.Model
		m.MessageID = rec.Message.ID
		if rec.Message.Usage != nil {
			m.Usage = *rec.Message.Usage
		}
		blocks, isList, _ := DecodeContentBlocks(rec.Message.Content)
		if !isList {
			m.AssistantText = StringContent(rec.Message.Content)
			m.Text = m.AssistantText
			break
		}
		var sb, at strings.Builder
		for _, b := range blocks {
			switch b.Type {
			case "text":
				sb.WriteString(b.Text)
				sb.WriteString("\n")
				at.WriteString(b.Text)
				at.WriteString("\n")
			case "tool_use":
				m.ToolUses = append(m.ToolUses, b)
				sb.WriteString("[tool_use ")
				sb.WriteString(b.Name)
				sb.WriteString("]\n")
			}
		}
		m.Text = strings.TrimSpace(sb.String())
		m.AssistantText = strings.TrimSpace(at.String())
	case "system":
		m.Role = "system"
		m.Subtype = rec.Subtype
		m.Text = StringContent(rec.Content)
	default:
		m.Role = "meta"
		m.Subtype = rec.Type
	}
	return m
}

// StringifyResult reduces a tool_result content (a string or a list of
// {type:"text"} blocks) to plain text.
func StringifyResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var list []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &list); err == nil {
		var sb strings.Builder
		for _, it := range list {
			if it.Type == "text" {
				sb.WriteString(it.Text)
				sb.WriteString("\n")
			}
		}
		return strings.TrimSpace(sb.String())
	}
	return string(raw)
}
