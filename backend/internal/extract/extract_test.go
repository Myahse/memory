package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunk(t *testing.T) {
	if got := Chunk("short text", 100, 10); len(got) != 1 {
		t.Fatalf("got %d chunks", len(got))
	}
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("This is sentence number one of a long document about laptops. ")
		if i%10 == 9 {
			b.WriteString("\n\n")
		}
	}
	chunks := Chunk(b.String(), 500, 80)
	if len(chunks) < 10 {
		t.Fatalf("expected many chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if n := utf8.RuneCountInString(c); n > 600 {
			t.Fatalf("chunk too long: %d", n)
		}
	}
	if Chunk("   ", 100, 10) != nil {
		t.Fatal("blank text should give no chunks")
	}
}

func TestDocx(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte(`<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>University</w:t></w:r><w:r><w:t> application</w:t></w:r></w:p><w:p><w:r><w:t>Deadline Friday</w:t></w:r></w:p></w:body></w:document>`))
	w, _ = zw.Create("docProps/core.xml")
	w.Write([]byte(`<cp:coreProperties xmlns:cp="c" xmlns:dc="d"><dc:title>Admissions</dc:title></cp:coreProperties>`))
	zw.Close()
	doc, err := Text(buf.Bytes(), "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Text != "University application\nDeadline Friday" || doc.Title != "Admissions" {
		t.Fatalf("got %q / %q", doc.Text, doc.Title)
	}
}

func TestUnsupported(t *testing.T) {
	if _, err := Text([]byte("x"), "application/x-msdownload"); err != ErrUnsupported {
		t.Fatalf("got %v", err)
	}
}

func TestHTMLText(t *testing.T) {
	text, title := HTMLText([]byte(`<html><head><title>HP Omen 16</title><script>evil()</script></head><body><p>RTX 5080</p><nav>menu</nav></body></html>`))
	if title != "HP Omen 16" || text != "RTX 5080" {
		t.Fatalf("got %q %q", text, title)
	}
}

func TestFetchLinkBlocksPrivateNetworks(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1/", "http://169.254.169.254/latest/meta-data", "http://10.0.0.1/", "file:///etc/passwd"} {
		if _, err := FetchLink(context.Background(), u); err == nil {
			t.Fatalf("%s should be refused", u)
		}
	}
}
