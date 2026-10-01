// Package extract pulls text out of documents and splits it into chunks.
package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/net/html"
)

var ErrUnsupported = errors.New("unsupported file type")

// DocumentMIMEs lists accepted document types.
var DocumentMIMEs = map[string]bool{
	"application/pdf": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"text/plain":       true,
	"text/markdown":    true,
	"text/csv":         true,
	"text/html":        true,
	"application/json": true,
	"application/rtf":  true,
	"text/rtf":         true,
}

type Document struct {
	Text  string
	Title string
	// Pages is the page count for PDFs.
	Pages int
}

// Text extracts text from a supported document.
func Text(data []byte, mime string) (*Document, error) {
	switch mime {
	case "application/pdf":
		return pdfText(data)
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return docxText(data)
	case "text/html":
		t, title := HTMLText(data)
		return &Document{Text: t, Title: title}, nil
	case "application/rtf", "text/rtf":
		return &Document{Text: rtfText(string(data))}, nil
	case "text/plain", "text/markdown", "text/csv", "application/json":
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("text file is not valid UTF-8")
		}
		return &Document{Text: string(data)}, nil
	}
	return nil, ErrUnsupported
}

func pdfText(data []byte) (doc *Document, err error) {
	defer func() { // the PDF library panics on some malformed files
		if r := recover(); r != nil {
			doc, err = nil, fmt.Errorf("could not read PDF: %v", r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("could not read PDF: %w", err)
	}
	var b strings.Builder
	n := r.NumPage()
	for i := 1; i <= n; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		rows, err := p.GetTextByRow()
		if err != nil {
			continue
		}
		for _, row := range rows {
			var line strings.Builder
			for _, w := range row.Content {
				line.WriteString(w.S)
			}
			if s := strings.TrimSpace(line.String()); s != "" {
				b.WriteString(s)
				b.WriteByte('\n')
			}
		}
		b.WriteByte('\n')
	}
	title := ""
	if info := r.Trailer().Key("Info"); !info.IsNull() {
		title = strings.TrimSpace(info.Key("Title").Text())
	}
	return &Document{Text: strings.TrimSpace(b.String()), Title: title, Pages: n}, nil
}

func docxText(data []byte) (*Document, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("could not read DOCX: %w", err)
	}
	var body, core []byte
	for _, f := range zr.File {
		if f.Name != "word/document.xml" && f.Name != "docProps/core.xml" {
			continue
		}
		if f.UncompressedSize64 > 50<<20 {
			return nil, fmt.Errorf("DOCX content too large")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 50<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if f.Name == "word/document.xml" {
			body = b
		} else {
			core = b
		}
	}
	if body == nil {
		return nil, fmt.Errorf("DOCX has no document body")
	}
	var out strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(body))
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("could not parse DOCX: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				out.WriteByte('\t')
			case "br", "cr":
				out.WriteByte('\n')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				out.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				out.Write(t)
			}
		}
	}
	doc := &Document{Text: strings.TrimSpace(out.String())}
	if core != nil {
		var meta struct {
			Title string `xml:"title"`
		}
		if xml.Unmarshal(core, &meta) == nil {
			doc.Title = strings.TrimSpace(meta.Title)
		}
	}
	return doc, nil
}

var rtfCtrl = regexp.MustCompile(`\\[a-z]+-?\d* ?|[{}]|\\'[0-9a-f]{2}`)

func rtfText(s string) string {
	s = strings.ReplaceAll(s, `\par`, "\n")
	return strings.TrimSpace(rtfCtrl.ReplaceAllString(s, ""))
}

// HTMLText returns the visible text and <title> of an HTML document.
func HTMLText(data []byte) (string, string) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", ""
	}
	var b strings.Builder
	title := ""
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg", "nav", "footer", "header", "form", "iframe":
				return
			case "title":
				if n.FirstChild != nil && title == "" {
					title = strings.TrimSpace(n.FirstChild.Data)
				}
				return
			}
		}
		if n.Type == html.TextNode {
			if t := strings.Join(strings.Fields(n.Data), " "); t != "" {
				b.WriteString(t)
				b.WriteByte(' ')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "li", "h1", "h2", "h3", "h4", "h5", "h6", "br", "tr", "section", "article":
				b.WriteByte('\n')
			}
		}
	}
	walk(doc)
	lines := strings.Split(b.String(), "\n")
	var out []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n"), title
}
