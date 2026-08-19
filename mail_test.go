package main

import (
	"bytes"
	"net/mail"
	"os"
	"strings"
	"testing"
)

// leseMail baut aus einem Rohtext eine Mail und liefert den extrahierten Text.
func leseMail(t *testing.T, roh string) (betreff string, text string) {
	t.Helper()

	m, err := mail.ReadMessage(strings.NewReader(strings.ReplaceAll(roh, "\n", "\r\n")))

	if err != nil {
		t.Fatalf("Mail nicht lesbar: %v", err)
	}

	return decodeSubject(m.Header.Get("Subject")), extractBody(m.Header, m.Body, true)
}

func TestBetreffWirdMimeDekodiert(t *testing.T) {
	betreff := decodeSubject("=?utf-8?b?U21va2UtVGVzdCBtdWx0aXBhcnQgw6TDtsO8?=")

	if betreff != "Smoke-Test multipart äöü" {
		t.Errorf("Betreff = %q", betreff)
	}
}

func TestBetreffOhneKodierungBleibtUnveraendert(t *testing.T) {
	if got := decodeSubject("[TestNAS] Test Message"); got != "[TestNAS] Test Message" {
		t.Errorf("Betreff = %q", got)
	}
}

func TestBetreffLatin1(t *testing.T) {
	if got := decodeSubject("=?iso-8859-1?q?Datentr=E4ger?="); got != "Datenträger" {
		t.Errorf("Betreff = %q", got)
	}
}

// Der Fall, an dem der Upstream eine Base64-Wurst ausgeliefert hat.
func TestEinfacheMailBase64(t *testing.T) {
	_, text := leseMail(t, `Subject: Test
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: base64

VGVzdG5hY2hyaWNodCBtaXQgVW1sYXV0ZW46IMOkw7bDvMOf
`)

	if text != "Testnachricht mit Umlauten: äöüß" {
		t.Errorf("Text = %q", text)
	}
}

func TestEinfacheMailQuotedPrintable(t *testing.T) {
	_, text := leseMail(t, `Subject: Test
Content-Type: text/plain; charset=iso-8859-1
Content-Transfer-Encoding: quoted-printable

Datentr=E4ger fast voll
`)

	if text != "Datenträger fast voll" {
		t.Errorf("Text = %q", text)
	}
}

// Der QNAP-Fall: multipart, aber ohne text/plain-Teil. Upstream lieferte "".
func TestMultipartNurHTML(t *testing.T) {
	_, text := leseMail(t, `Subject: [TestNAS] Test Message
Content-Type: multipart/alternative; boundary="grenze"

--grenze
Content-Type: text/html; charset=utf-8

<html><body><table><tr><td>Volume:</td><td>DataVol1</td></tr>
<tr><td>Status:</td><td>Degraded</td></tr></table></body></html>
--grenze--
`)

	if !strings.Contains(text, "DataVol1") || !strings.Contains(text, "Degraded") {
		t.Fatalf("Text = %q", text)
	}

	if strings.Contains(text, "<") {
		t.Errorf("HTML nicht abgebaut: %q", text)
	}

	// Tabellenzeilen duerfen nicht zu einer Zeile verschmelzen.
	if !strings.Contains(text, "\n") {
		t.Errorf("keine Zeilenstruktur: %q", text)
	}

	// Zellen innerhalb einer Zeile brauchen einen Trenner.
	if !strings.Contains(text, "Volume: DataVol1") {
		t.Errorf("Zellen ohne Trenner: %q", text)
	}
}

// Sind beide Varianten da, gewinnt text/plain -- auch wenn HTML zuerst kommt.
func TestMultipartBevorzugtPlain(t *testing.T) {
	_, text := leseMail(t, `Subject: Test
Content-Type: multipart/alternative; boundary="grenze"

--grenze
Content-Type: text/html; charset=utf-8

<html><body>HTML-Variante</body></html>
--grenze
Content-Type: text/plain; charset=utf-8

Klartext-Variante
--grenze--
`)

	if text != "Klartext-Variante" {
		t.Errorf("Text = %q", text)
	}
}

// Nur-HTML ohne multipart: Upstream reichte rohes Markup durch.
func TestNichtMultipartHTML(t *testing.T) {
	_, text := leseMail(t, `Subject: Warnung
Content-Type: text/html; charset=utf-8

<html><body><h1>Warnung</h1><p>Datentraeger fast voll.</p></body></html>
`)

	if text != "Warnung\nDatentraeger fast voll." {
		t.Errorf("Text = %q", text)
	}
}

func TestVerschachteltesMultipart(t *testing.T) {
	_, text := leseMail(t, `Subject: Test
Content-Type: multipart/related; boundary="aussen"

--aussen
Content-Type: multipart/alternative; boundary="innen"

--innen
Content-Type: text/plain; charset=utf-8

Innerer Klartext
--innen--
--aussen--
`)

	if text != "Innerer Klartext" {
		t.Errorf("Text = %q", text)
	}
}

func TestLeererBodyFaelltAufBetreffZurueck(t *testing.T) {
	// extractBody liefert hier "", der Rueckfall passiert in Session.Data.
	_, text := leseMail(t, `Subject: [TestNAS] Test Message
Content-Type: multipart/alternative; boundary="grenze"

--grenze
Content-Type: image/png
Content-Transfer-Encoding: base64

iVBORw0KGgo=
--grenze--
`)

	if text != "" {
		t.Errorf("Text = %q, erwartet leer", text)
	}
}

func TestHtmlToTextEntitiesUndScript(t *testing.T) {
	got := htmlToText(`<style>p{color:red}</style><p>Platz&nbsp;belegt: 95&nbsp;%</p>` +
		`<script>alert("x")</script><p>M&uuml;ll &amp; Reste</p>`)

	if got != "Platz belegt: 95 %\nMüll & Reste" {
		t.Errorf("Text = %q", got)
	}
}

func TestHtmlToTextUnvollstaendigesTag(t *testing.T) {
	if got := htmlToText("<p>Anfang<p>Ende<div"); got != "Anfang\nEnde" {
		t.Errorf("Text = %q", got)
	}
}

func TestCp1252Sonderzeichen(t *testing.T) {
	// 0x92 ist in Windows-1252 ein typografischer Apostroph, in Latin-1 unbelegt.
	if got := toUTF8([]byte{'I', 't', 0x92, 's'}, "windows-1252"); got != "It’s" {
		t.Errorf("Text = %q", got)
	}
}

// Der Ernstfall: die echte Testmail der QNAP (mitgeschnitten am 2026-08-19).
// Sie traegt Outlook-Conditional-Comments, Webfont-Links und eine Layout-Tabelle.
func TestEchteQnapTestmail(t *testing.T) {
	roh, err := os.ReadFile("testdata/qnap-test-message.eml")

	if err != nil {
		t.Skipf("Mitschnitt nicht vorhanden: %v", err)
	}

	m, err := mail.ReadMessage(bytes.NewReader(roh))

	if err != nil {
		t.Fatalf("Mail nicht lesbar: %v", err)
	}

	betreff := decodeSubject(m.Header.Get("Subject"))
	text := extractBody(m.Header, m.Body, true)

	if betreff != "[TestNAS] Test Message" {
		t.Errorf("Betreff = %q", betreff)
	}

	for _, erwartet := range []string{"NAS Name: TestNAS", "This is a test message"} {
		if !strings.Contains(text, erwartet) {
			t.Errorf("%q fehlt in: %q", erwartet, text)
		}
	}

	// Genau hieran ist der Parser vorher gescheitert: die Outlook-DPI-Angabe
	// aus dem Conditional Comment landete als erste Zeile im Push.
	for _, zeile := range strings.Split(text, "\n") {
		if zeile == "96" {
			t.Errorf("Kommentar-Artefakt im Text: %q", text)
		}
	}

	if strings.Contains(text, "<") || strings.Contains(text, "@media") {
		t.Errorf("Markup nicht abgebaut: %q", text)
	}
}
