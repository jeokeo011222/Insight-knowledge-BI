package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// maxBase64Line is the RFC 2045 line length limit for base64 payloads.
const maxBase64Line = 76

// buildMessage renders the complete RFC 5322 message.
func buildMessage(msg Message, from string, to []string) []byte {
	subject := strings.TrimSpace(msg.Subject)
	if subject == "" {
		subject = "(no subject)"
	}
	textBody := msg.TextBody
	htmlBody := msg.HTMLBody
	if textBody == "" && htmlBody != "" {
		textBody = stripHTML(htmlBody)
	}
	if htmlBody == "" && textBody != "" {
		htmlBody = "<pre>" + htmlEscape(textBody) + "</pre>"
	}

	var buf bytes.Buffer
	writeHeader(&buf, "From", from)
	writeHeader(&buf, "To", strings.Join(to, ", "))
	writeHeader(&buf, "Subject", encodeHeader(subject))
	writeHeader(&buf, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", messageID(from))
	writeHeader(&buf, "MIME-Version", "1.0")

	attachments := msg.Attachments
	if len(attachments) == 0 {
		writeHeader(&buf, "Content-Type", `multipart/alternative; boundary="alt"`)
		buf.WriteString("\r\n")
		writeAlternative(&buf, "alt", textBody, htmlBody)
		return buf.Bytes()
	}

	writeHeader(&buf, "Content-Type", `multipart/mixed; boundary="mix"`)
	buf.WriteString("\r\n")
	buf.WriteString("--mix\r\n")
	buf.WriteString("Content-Type: multipart/alternative; boundary=\"alt\"\r\n\r\n")
	writeAlternative(&buf, "alt", textBody, htmlBody)
	for _, att := range attachments {
		buf.WriteString("--mix\r\n")
		writeAttachment(&buf, att)
	}
	buf.WriteString("--mix--\r\n")
	return buf.Bytes()
}

// writeAlternative writes the text/plain + text/html alternative block.
func writeAlternative(buf *bytes.Buffer, boundary, textBody, htmlBody string) {
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64Lines(buf, []byte(textBody))
	buf.WriteString("\r\n")

	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64Lines(buf, []byte(htmlBody))
	buf.WriteString("\r\n")

	buf.WriteString("--" + boundary + "--\r\n\r\n")
}

// writeAttachment writes one base64 encoded attachment part.
func writeAttachment(buf *bytes.Buffer, att Attachment) {
	ct := att.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	name := att.Filename
	if name == "" {
		name = "attachment"
	}
	buf.WriteString("Content-Type: " + ct + "; " + param("name", name) + "\r\n")
	buf.WriteString("Content-Transfer-Encoding: base64\r\n")
	buf.WriteString("Content-Disposition: attachment; " + param("filename", name) + "\r\n\r\n")
	writeBase64Lines(buf, att.Data)
	buf.WriteString("\r\n")
}

// param renders a MIME parameter, using RFC 2231 encoding for non-ASCII values.
func param(key, value string) string {
	if isASCII(value) {
		return key + "=\"" + strings.ReplaceAll(value, `"`, "") + "\""
	}
	return key + "*=UTF-8''" + rfc2231(value)
}

// writeHeader writes a single header line.
func writeHeader(buf *bytes.Buffer, key, value string) {
	buf.WriteString(key + ": " + strings.ReplaceAll(value, "\r\n", " ") + "\r\n")
}

// writeBase64Lines writes base64 split at 76 columns with CRLF endings.
func writeBase64Lines(buf *bytes.Buffer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > maxBase64Line {
		buf.WriteString(encoded[:maxBase64Line])
		buf.WriteString("\r\n")
		encoded = encoded[maxBase64Line:]
	}
	if encoded != "" {
		buf.WriteString(encoded)
		buf.WriteString("\r\n")
	}
}

// encodeHeader encodes a non-ASCII header value as RFC 2047 UTF-8 base64
// words, folding long values onto continuation lines.
func encodeHeader(value string) string {
	if isASCII(value) {
		return value
	}
	const chunk = 45 // 45 bytes -> 60 base64 chars, stays under the 75 limit
	raw := []byte(value)
	words := make([]string, 0, len(raw)/chunk+1)
	for len(raw) > chunk {
		words = append(words, "=?UTF-8?B?"+base64.StdEncoding.EncodeToString(raw[:chunk])+"?=")
		raw = raw[chunk:]
	}
	if len(raw) > 0 {
		words = append(words, "=?UTF-8?B?"+base64.StdEncoding.EncodeToString(raw)+"?=")
	}
	return strings.Join(words, "\r\n ")
}

// messageID generates a unique Message-ID for the sender domain.
func messageID(from string) string {
	domain := "superset.local"
	if i := strings.LastIndex(from, "@"); i >= 0 && i+1 < len(from) {
		domain = strings.Trim(from[i+1:], "> ")
	}
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), domain)
	}
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), hex.EncodeToString(buf), domain)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

// rfc2231 percent-encodes a parameter value.
func rfc2231(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteString("%")
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

// stripHTML produces a crude plain text fallback for HTML bodies.
func stripHTML(s string) string {
	var out strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	lines := strings.Split(out.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(strings.Join(filterEmpty(lines), "\n"))
}

func filterEmpty(lines []string) []string {
	out := lines[:0]
	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
