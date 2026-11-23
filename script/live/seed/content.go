package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime"
	"mime/quotedprintable"
	"strings"
	"time"
)

// person is a made-up correspondent or contact.
type person struct {
	Given, Family, Company, Title, City, Country string
}

func (p person) Name() string { return p.Given + " " + p.Family }

// Email is an address at a reserved example domain, so nothing is ever
// delivered to it.
func (p person) Email() string {
	return strings.ToLower(asciiOnly(p.Given)+"."+asciiOnly(p.Family)) + "@example.com"
}

func asciiOnly(s string) string {
	var b strings.Builder

	for _, r := range s {
		if r < 128 && r != ' ' && r != '\'' {
			b.WriteRune(r)
		}
	}

	return cmpOr(b.String(), "x")
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

var people = []person{
	{"Aino", "Virtanen", "Kide Oy", "Product Manager", "Helsinki", "Finland"},
	{"Mikko", "Korhonen", "Kide Oy", "Engineer", "Espoo", "Finland"},
	{"Sofia", "Lindqvist", "Nordvik AB", "CFO", "Stockholm", "Sweden"},
	{"José", "García Márquez", "Solaris SL", "Designer", "Madrid", "Spain"},
	{"Zoë", "O'Brien", "Quay & Co", "Solicitor", "Dublin", "Ireland"},
	{"Hiroshi", "Tanaka", "Sakura KK", "Director", "Tokyo", "Japan"},
	{"Ананд", "Кумар", "", "", "Moscow", "Russia"},
	{"李", "娜", "Hua Tech", "Researcher", "Shanghai", "China"},
	{"Fatima", "Al-Sayed", "Oasis LLC", "Consultant", "Dubai", "UAE"},
	{"Grace", "Okafor", "Lagos Labs", "CTO", "Lagos", "Nigeria"},
	{"Liam", "Smith", "", "", "Toronto", "Canada"},
	{"Emma", "Müller", "Bergwerk GmbH", "Accountant", "München", "Germany"},
	{"Noah", "Johnson", "Acme Inc.", "Sales", "Austin", "USA"},
	{"Olivia", "Brown", "Acme Inc.", "Support; Tier 2", "Austin", "USA"},
	{"Łukasz", "Wójcik", "Wisła S.A.", "Architect", "Kraków", "Poland"},
	{"Chloé", "Dubois", "Maison Bleue", "Chef", "Lyon", "France"},
}

var subjects = []string{
	"Quarterly planning", "Lunch on Friday?", "Invoice #%d", "Re: the draft contract",
	"Trip to Lapland ❄️", "Photos from the weekend", "Budget, v2; please review",
	"Meeting notes — %d", "Kiitos avusta!", "日本出張の件", "Quick question",
	"Your order %d has shipped", "Birthday party 🎉", "Server outage post-mortem",
	"A very long subject line that goes on and on to check folding of headers in the archive files %d",
}

var paragraphs = []string{
	"Thanks for the update. I had a look at the numbers and they seem fine to me.",
	"Could we move the meeting to Thursday afternoon? Tuesday is packed.",
	"Attached is the file we talked about. Let me know if anything is missing.",
	"Kiitos viestistä! Palataan asiaan ensi viikolla.",
	"ご連絡ありがとうございます。来週またご連絡します。",
	"Line with trailing spaces   \nand a line starting with From to test mbox escaping:\nFrom here on it is fine.",
	"Price: 100 € — about £85 or $110. Math: 2 × 3 ≠ 7, α β γ.",
	"-- \nThis looks like a signature but is in the middle.",
	"A long line: " + strings.Repeat("lorem ipsum dolor sit amet ", 20),
}

// subject returns a random subject, numbered where it has a %d.
func (s *seeder) subject() string {
	subj := pick(s, subjects)
	if strings.Contains(subj, "%d") {
		subj = fmt.Sprintf(subj, 1000+s.rng.IntN(9000))
	}

	return subj
}

// body returns one to four random paragraphs.
func (s *seeder) body() string {
	n := 1 + s.rng.IntN(4)
	parts := make([]string, n)

	for i := range parts {
		parts[i] = pick(s, paragraphs)
	}

	return strings.Join(parts, "\n\n")
}

// htmlBody renders plain text as simple HTML.
func htmlBody(text string) string {
	var b strings.Builder

	b.WriteString("<html><body>")

	for p := range strings.SplitSeq(text, "\n\n") {
		p = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\n", "<br>").Replace(p)
		b.WriteString("<p>" + p + "</p>")
	}

	b.WriteString("<p style=\"color:#888\"><i>Sent from the pimvault seeder</i></p></body></html>")

	return b.String()
}

// attachment is a file attached to a message.
type attachment struct {
	Name, ContentType string
	Data              []byte
	// ContentID makes it an inline image referenced from the HTML body.
	ContentID string
}

// message is one mail message in a thread.
type message struct {
	From, To     person
	FromAddr     string
	ToAddr       string
	Cc           []person
	Subject      string
	Text         string
	HTML         bool
	Date         time.Time
	ID           string
	InReplyTo    string
	References   []string
	Attachments  []attachment
	Unread, Flag bool
}

// mime renders a message as RFC 5322 bytes.
func (m *message) mime() []byte {
	var b bytes.Buffer

	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }

	header("From", address(m.From.Name(), m.FromAddr))
	header("To", address(m.To.Name(), m.ToAddr))

	if len(m.Cc) > 0 {
		cc := make([]string, len(m.Cc))
		for i, p := range m.Cc {
			cc[i] = address(p.Name(), p.Email())
		}

		header("Cc", strings.Join(cc, ", "))
	}

	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", m.Date.Format(time.RFC1123Z))
	header("Message-ID", m.ID)

	if m.InReplyTo != "" {
		header("In-Reply-To", m.InReplyTo)
		header("References", strings.Join(m.References, " "))
	}

	header("MIME-Version", "1.0")

	textPart := part("text/plain; charset=utf-8", "", qp(m.Text))

	var content []byte

	switch {
	case !m.HTML && len(m.Attachments) == 0:
		content = textPart
	default:
		body := textPart
		if m.HTML {
			body = multipart("alternative", textPart, m.htmlPart())
		}

		parts := [][]byte{body}

		for _, a := range m.Attachments {
			if a.ContentID == "" {
				parts = append(parts, part(a.ContentType+"; name=\""+a.Name+"\"",
					"Content-Disposition: attachment; filename=\""+a.Name+"\"\r\nContent-Transfer-Encoding: base64\r\n", b64(a.Data)))
			}
		}

		content = body
		if len(parts) > 1 {
			content = multipart("mixed", parts...)
		}
	}

	b.Write(content)

	return b.Bytes()
}

// htmlPart is the HTML alternative, with its inline images when there are
// any.
func (m *message) htmlPart() []byte {
	html := htmlBody(m.Text)

	var inline [][]byte

	for _, a := range m.Attachments {
		if a.ContentID != "" {
			html = strings.Replace(html, "</body>", "<img src=\"cid:"+a.ContentID+"\" alt=\""+a.Name+"\"></body>", 1)
			inline = append(inline, part(a.ContentType,
				"Content-ID: <"+a.ContentID+">\r\nContent-Disposition: inline; filename=\""+a.Name+"\"\r\nContent-Transfer-Encoding: base64\r\n",
				b64(a.Data)))
		}
	}

	htmlPart := part("text/html; charset=utf-8", "", qp(html))
	if len(inline) == 0 {
		return htmlPart
	}

	return multipart("related", append([][]byte{htmlPart}, inline...)...)
}

func address(name, addr string) string {
	return mime.QEncoding.Encode("utf-8", name) + " <" + addr + ">"
}

// part renders a MIME part: its Content-Type, extra header lines and body.
// Bodies without an encoding header are quoted-printable.
func part(contentType, extra string, body []byte) []byte {
	if extra == "" {
		extra = "Content-Transfer-Encoding: quoted-printable\r\n"
	}

	return append([]byte("Content-Type: "+contentType+"\r\n"+extra+"\r\n"), body...)
}

// multipart renders parts as one multipart entity, header included.
func multipart(subtype string, parts ...[]byte) []byte {
	boundary := randomBoundary()

	var b bytes.Buffer

	fmt.Fprintf(&b, "Content-Type: multipart/%s; boundary=\"%s\"\r\n\r\n", subtype, boundary)

	for _, p := range parts {
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		b.Write(p)
		b.WriteString("\r\n")
	}

	fmt.Fprintf(&b, "--%s--\r\n", boundary)

	return b.Bytes()
}

func randomBoundary() string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)

	return "seed-" + base64.RawURLEncoding.EncodeToString(buf)
}

// qp encodes text as quoted-printable with CRLF line ends.
func qp(text string) []byte {
	var b bytes.Buffer

	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n"))) //nolint:errcheck // writes to a buffer.
	_ = w.Close()                                                  //nolint:errcheck // as above.

	return b.Bytes()
}

// b64 encodes data as base64 in 76-character lines.
func b64(data []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(data)

	var b bytes.Buffer

	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}

	b.WriteString(enc + "\r\n")

	return b.Bytes()
}

// picture draws a size×size gradient; seed varies the colors.
func picture(size, seed int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	for x := range size {
		for y := range size {
			img.Set(x, y, color.RGBA{uint8((x + seed*40) % 256), uint8((y + seed*70) % 256), uint8((x*y/size + seed*90) % 256), 255}) //nolint:gosec // G115: wraps on purpose.
		}
	}

	return img
}

func jpegImage(size, seed int) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, picture(size, seed), &jpeg.Options{Quality: 85}) //nolint:errcheck // writes to a buffer.

	return b.Bytes()
}

func pngImage(size, seed int) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, picture(size, seed)) //nolint:errcheck // writes to a buffer.

	return b.Bytes()
}

// attachments returns zero to two random attachments.
func (s *seeder) attachments(inlineOK bool) []attachment {
	var out []attachment

	if s.chance(0.25) {
		out = append(out, pick(s, []attachment{
			{Name: "notes.txt", ContentType: "text/plain", Data: []byte("Plain notes.\r\nSecond line: åäö.\r\n")},
			{Name: "report.pdf", ContentType: "application/pdf", Data: fakePDF()},
			{Name: "photo.jpg", ContentType: "image/jpeg", Data: jpegImage(320, s.rng.IntN(8))},
			{Name: "Tilastot 2026 – ääkköset.csv", ContentType: "text/csv", Data: []byte("kuukausi;summa\r\ntammikuu;12,50\r\n")},
		}))
	}

	if inlineOK && s.chance(0.1) {
		out = append(out, attachment{Name: "logo.png", ContentType: "image/png", Data: pngImage(64, s.rng.IntN(8)), ContentID: "logo@seed"})
	}

	return out
}

// fakePDF is a minimal valid one-page PDF.
func fakePDF() []byte {
	return []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")
}
