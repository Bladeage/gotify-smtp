package main

// Ergaenzungen zum Upstream-Plugin (tystuyfzand/gotify-smtp).
//
// Der Upstream wertet ausschliesslich text/plain aus, dekodiert den Betreff nicht
// und ignoriert Content-Transfer-Encoding ausserhalb von multipart-Mails. Reale
// Absender -- QNAP QTS, Drucker, USVs, Fritz!Box -- verschicken aber HTML-Mails
// mit MIME-kodierten Betreffs. Diese Datei ergaenzt genau das und kommt ohne
// zusaetzliche Abhaengigkeiten aus, damit der Plugin-Build offline reproduzierbar
// bleibt.

import (
	"bytes"
	"encoding/base64"
	"html"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// decodeSubject dekodiert MIME-kodierte Wortfolgen ("=?utf-8?b?...?=") im Betreff.
// Laesst sich ein Wort nicht dekodieren, wird der Rohwert beibehalten -- ein
// unschoener Betreff ist immer noch besser als eine verworfene Benachrichtigung.
func decodeSubject(raw string) string {
	dec := &mime.WordDecoder{CharsetReader: charsetReader}

	decoded, err := dec.DecodeHeader(raw)
	if err != nil {
		return raw
	}

	return decoded
}

// extractBody holt den Nachrichtentext aus einer Mail: bevorzugt text/plain,
// ersatzweise aus text/html gewonnener Klartext.
func extractBody(header mail.Header, body io.Reader) string {
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))

	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		return ParsePart(body, params["boundary"])
	}

	b, err := ioutil.ReadAll(body)
	if err != nil {
		return ""
	}

	text := decodeContent(b, header.Get("Content-Transfer-Encoding"), params["charset"])

	if strings.HasPrefix(mediaType, "text/html") {
		return htmlToText(text)
	}

	return strings.TrimSpace(text)
}

// decodeContent macht aus dem Rohinhalt eines Mailteils lesbaren UTF-8-Text,
// indem es Transfer-Encoding und Zeichensatz aufloest.
func decodeContent(raw []byte, encoding, charset string) string {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		// Fehler bewusst tolerant behandeln: base64.StdEncoding bricht bei
		// Zeilenumbruechen ab, die in Mails ueblich sind.
		cleaned := strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(string(raw))
		if decoded, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
			raw = decoded
		}
	case "quoted-printable":
		if decoded, err := ioutil.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw))); err == nil {
			raw = decoded
		}
	}

	return toUTF8(raw, charset)
}

// toUTF8 wandelt die gaengigen Ein-Byte-Zeichensaetze nach UTF-8. Alles andere
// wird unveraendert durchgereicht -- lieber ein paar falsche Zeichen als eine
// zusaetzliche Abhaengigkeit im Plugin-Build.
func toUTF8(raw []byte, charset string) string {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return string(raw)
	case "iso-8859-1", "iso8859-1", "latin1", "iso-8859-15", "windows-1252", "cp1252":
		var sb strings.Builder
		for _, b := range raw {
			if r, ok := cp1252Extra[b]; ok {
				sb.WriteRune(r)
				continue
			}
			sb.WriteRune(rune(b))
		}
		return sb.String()
	}

	if utf8.Valid(raw) {
		return string(raw)
	}

	return string(raw)
}

// cp1252Extra enthaelt die Zeichen, in denen sich Windows-1252 von Latin-1
// unterscheidet (0x80-0x9F). Genau dort sitzen typografische Anfuehrungszeichen
// und Gedankenstriche, die Mailclients gerne verwenden.
var cp1252Extra = map[byte]rune{
	0x80: '€', 0x82: '‚', 0x83: 'ƒ', 0x84: '„', 0x85: '…', 0x86: '†', 0x87: '‡',
	0x88: 'ˆ', 0x89: '‰', 0x8A: 'Š', 0x8B: '‹', 0x8C: 'Œ', 0x8E: 'Ž', 0x91: '‘',
	0x92: '’', 0x93: '“', 0x94: '”', 0x95: '•', 0x96: '–', 0x97: '—', 0x98: '˜',
	0x99: '™', 0x9A: 'š', 0x9B: '›', 0x9C: 'œ', 0x9E: 'ž', 0x9F: 'Ÿ',
}

// charsetReader bedient den WordDecoder fuer Betreffs in Nicht-UTF-8-Zeichensaetzen.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	b, err := ioutil.ReadAll(input)
	if err != nil {
		return nil, err
	}

	return strings.NewReader(toUTF8(b, charset)), nil
}

// blockElements sind Tags, an denen beim HTML-Abbau ein Zeilenumbruch entsteht,
// damit Tabellenzeilen und Absaetze nicht zu einer Textwurst verschmelzen.
var blockElements = map[string]bool{
	"br": true, "p": true, "div": true, "tr": true, "li": true, "h1": true,
	"h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "table": true,
	"ul": true, "ol": true, "blockquote": true, "hr": true, "section": true,
}

// cellElements trennen innerhalb einer Zeile, statt eine neue zu beginnen.
var cellElements = map[string]bool{"td": true, "th": true}

// htmlToText gewinnt aus HTML lesbaren Klartext: Tags fallen weg, Blockelemente
// werden zu Zeilenumbruechen, script/style-Inhalte werden verworfen, Entities
// aufgeloest. Das ist bewusst ein Abbau und kein Parser -- fuer Geraetemails
// mit ihrem simplen Tabellen-HTML reicht das.
func htmlToText(input string) string {
	var sb strings.Builder

	for i := 0; i < len(input); {
		c := input[i]

		if c != '<' {
			sb.WriteByte(c)
			i++
			continue
		}

		// Kommentare vor der Tag-Erkennung abfangen: Conditional Comments wie
		// <!--[if gte mso 9]><xml>...96...</xml><![endif]--> enthalten ein '>',
		// an dem die normale Tag-Erkennung zu frueh abbraeche -- der Inhalt
		// landete dann als Text in der Benachrichtigung.
		if strings.HasPrefix(input[i:], "<!--") {
			if idx := strings.Index(input[i+4:], "-->"); idx >= 0 {
				i += 4 + idx + 3
				continue
			}

			// Unabgeschlossener Kommentar: der Rest ist Markup-Muell.
			break
		}

		end := strings.IndexByte(input[i:], '>')
		if end < 0 {
			// Unabgeschlossenes Tag: Rest ist kein verwertbares Markup mehr.
			break
		}

		tag := input[i+1 : i+end]
		i += end + 1

		name := tagName(tag)

		// Inhalt von script/style/title gehoert nicht in eine Benachrichtigung.
		if name == "script" || name == "style" || name == "title" {
			closing := "</" + name
			if idx := indexFold(input[i:], closing); idx >= 0 {
				i += idx
				if e := strings.IndexByte(input[i:], '>'); e >= 0 {
					i += e + 1
				}
			}
			continue
		}

		if blockElements[name] {
			sb.WriteByte('\n')
			continue
		}

		// Tabellenzellen sonst aneinanderkleben ("Volume:DataVol1") -- genau so
		// bauen QNAP & Co. ihre Meldungen auf.
		if cellElements[name] {
			sb.WriteByte(' ')
		}
	}

	return collapse(html.UnescapeString(sb.String()))
}

// tagName liefert den kleingeschriebenen Elementnamen aus einem Tag-Inneren
// ("/tr class=x" -> "tr").
func tagName(tag string) string {
	tag = strings.TrimSpace(tag)
	tag = strings.TrimPrefix(tag, "/")

	for i := 0; i < len(tag); i++ {
		switch tag[i] {
		case ' ', '\t', '\r', '\n', '/', '>':
			return strings.ToLower(tag[:i])
		}
	}

	return strings.ToLower(tag)
}

// indexFold sucht case-insensitiv, weil HTML-Tags in beliebiger Schreibweise kommen.
func indexFold(s, substr string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(substr))
}

// collapse raeumt den beim Tag-Abbau entstehenden Weissraum auf: Zeilen werden
// getrimmt, Leerzeichenketten zusammengefasst und Leerzeilen verworfen -- in einer
// Push-Nachricht kosten sie nur Platz.
func collapse(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\u00a0", " ")

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")

		if line == "" {
			continue
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

// ParsePart durchsucht einen multipart-Body nach verwertbarem Text. Bevorzugt
// wird text/plain; existiert kein solcher Teil -- QNAP QTS etwa verschickt reine
// HTML-Mails -- wird aus text/html Klartext gewonnen.
func ParsePart(body io.Reader, boundary string) string {
	plain, fromHTML := parseParts(body, boundary)

	if plain != "" {
		return plain
	}

	return fromHTML
}

// parseParts liefert Klartext- und HTML-Variante getrennt zurueck, damit der
// Aufrufer text/plain bevorzugen kann, auch wenn der HTML-Teil zuerst kommt.
func parseParts(body io.Reader, boundary string) (plain string, fromHTML string) {
	reader := multipart.NewReader(body, boundary)

	for {
		part, err := reader.NextPart()

		// io.EOF wie jeden anderen Fehler behandeln: was bis hier gelesen wurde,
		// ist besser als eine leere Benachrichtigung.
		if err != nil {
			break
		}

		mediaType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))

		if err != nil {
			// Ohne Content-Type gilt laut RFC 2045 text/plain.
			if part.Header.Get("Content-Type") != "" {
				continue
			}

			mediaType, params = "text/plain", map[string]string{}
		}

		if strings.HasPrefix(mediaType, "multipart/") {
			nestedPlain, nestedHTML := parseParts(part, params["boundary"])

			if plain == "" {
				plain = nestedPlain
			}

			if fromHTML == "" {
				fromHTML = nestedHTML
			}

			continue
		}

		isPlain := strings.HasPrefix(mediaType, "text/plain")
		isHTML := strings.HasPrefix(mediaType, "text/html")

		if !isPlain && !isHTML {
			continue
		}

		// Bereits gefundene Variante nicht erneut einlesen (Anhaenge koennen gross sein).
		if (isPlain && plain != "") || (isHTML && fromHTML != "") {
			continue
		}

		b, err := ioutil.ReadAll(part)

		if err != nil {
			continue
		}

		text := decodeContent(b, part.Header.Get("Content-Transfer-Encoding"), params["charset"])

		if isPlain {
			plain = strings.TrimSpace(text)
		} else {
			fromHTML = htmlToText(text)
		}
	}

	return plain, fromHTML
}
