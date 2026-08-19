# Was dieser Fork gegenueber dem Original aendert

Original: [tystuyfzand/gotify-smtp](https://github.com/tystuyfzand/gotify-smtp),
Stand `9ae41a4`. Das Original nimmt Mails zuverlaessig entgegen, laesst aber jeden
Absender an jedes Konto zustellen und wertet nur `text/plain` aus. Reale Geraetemails
— NAS, Drucker, USV, Router — kommen dadurch leer, als Base64-Wurst oder als rohes
HTML an.

## Betrieb und Sicherheit

| Vorher | Jetzt |
|---|---|
| Kein Passwort geprueft: wer einen Benutzernamen kennt, kann zustellen | Optionales Passwort je Benutzer, Vergleich in konstanter Zeit. Leer gelassen bleibt das alte Verhalten |
| Jeder Absender erlaubt | `allowed_senders` je Benutzer, als volle Adresse oder `@domain` |
| Port fest auf `:1025` verdrahtet | `listen_addr` konfigurierbar (instanzweit — der zuletzt speichernde Benutzer gewinnt) |
| Keine Bedienhinweise in der Oberflaeche | `GetDisplay` zeigt Adresse, Benutzername und Beispiel direkt im Plugin |
| Prioritaet immer 0, also lautlos | `priority` je Benutzer (0-10) |

## Auswertung der Mails

| # | Vorher | Jetzt | Ausloeser in der Praxis |
|---|---|---|---|
| 1 | Betreff roh: `=?utf-8?b?...?=` | `mime.WordDecoder` mit Charset-Reader | jede Mail mit Umlaut im Betreff |
| 2 | `Content-Transfer-Encoding` nur ausserhalb von multipart beachtet | einheitlich in beiden Wegen | Base64/quoted-printable von einfachen Absendern |
| 3 | Ohne `text/plain`-Teil bleibt die Nachricht **leer** | Rueckfall auf `text/html` mit Tag-Abbau | QNAP QTS verschickt multipart *ohne* Klartextteil |
| 4 | HTML-Kommentare mit `>` darin beenden den Tag-Abbau zu frueh, ihr Inhalt landet als Text im Push | Kommentare werden bis `-->` uebersprungen | Outlook-Conditional-Comments (`<o:PixelsPerInch>96</...>`) |
| 5 | Tabellenzellen kleben aneinander (`Volume:DataVol1`) | `td`/`th` werden zu Leerzeichen, Blockelemente zu Zeilenumbruechen | Geraetemails bestehen fast immer aus Layout-Tabellen |
| 6 | Kein Zeichensatz-Umgang | UTF-8, ASCII, Latin-1 und Windows-1252 (inkl. der cp1252-Sonderzeichen) | deutschsprachige Geraete |
| 7 | Betreff wird als erste Textzeile wiederholt | Wiederholung wird entfernt | QNAP QTS und aehnliche |
| 8 | leerer Text wird als leere Meldung zugestellt | Rueckfall auf den Betreff | Mails, die nur Anhaenge tragen |

Die Zeichensatz-Umsetzung kommt bewusst **ohne** `golang.org/x/text` aus, damit der
Plugin-Build keine zusaetzliche Abhaengigkeit bekommt und offline reproduzierbar bleibt.

Der HTML-Abbau ist ein Abbau, kein Parser: Tags fallen weg, `script`/`style`/`title`
werden verworfen, Entities aufgeloest. Fuer das simple Tabellen-HTML von Geraetemails
reicht das; fuer beliebiges Web-HTML ist es nicht gedacht. Mit `strip_html: false`
laesst sich das Markup unveraendert durchreichen.

## Konfiguration

Gotify zeigt fuer das Plugin einen YAML-Editor (Plugins → SMTP → Konfiguration).
Die Werte gelten **pro Gotify-Benutzer**, mit Ausnahme von `listen_addr`:

```yaml
password: ""                 # leer = jedes Passwort wird akzeptiert (Verhalten des Originals)
priority: 0                  # 0-10; ab 8 laesst die Android-App das Geraet klingeln
default_title: (no subject)  # Titel, wenn die Mail keinen Betreff traegt
strip_html: true             # HTML-Mails in lesbaren Text umsetzen
strip_subject_prefix: false  # true entfernt ein fuehrendes "[NAS-Name] " aus dem Titel
allowed_senders: []          # z. B. ["alerts@example.com", "@example.com"]
listen_addr: ":1025"         # instanzweit
```

Ungueltige Werte werden abgelehnt, die vorherige Konfiguration bleibt bestehen.

## Bauen und testen

Das Plugin muss gegen **exakt dieselben Abhaengigkeiten** gebaut sein wie der Server,
sonst verweigert Gotify das Laden. `go.mod` ist deshalb gegen Gotify v3.0.0 gecappt
(`gotify-server-v3.mod` liegt als Referenz bei).

```bash
docker run --rm --user "$(id -u):$(id -g)" -v "$PWD:/proj" -w /proj \
  -e GOFLAGS=-mod=mod -e HOME=/tmp gotify/build:1.26.0-linux-amd64 \
  sh -c 'gofmt -l . && go vet ./... && go test ./... && \
         go build -mod=readonly -a -installsuffix cgo -buildmode=plugin -o smtp-linux-amd64.so .'
```

Wichtigster Test ist `TestDataMitEchterQnapMail`: er laeuft gegen
`testdata/qnap-test-message.eml` — die echte Testnachricht einer QNAP
(`multipart/mixed`, ein base64-kodierter `text/html`-Teil, Layout-Tabelle,
Outlook-Conditional-Comments), anonymisiert auf `TestNAS`/`example.com`. Fuer weitere
Geraete lohnt derselbe Weg: Originalmail einfangen, als Fixture ablegen, Erwartung
festschreiben.

## Weiterhin zu beachten

TLS gibt es nicht, angeboten wird nur `AUTH PLAIN`. Selbst mit gesetztem Passwort geht
es also im Klartext ueber die Leitung. Der Port gehoert ins lokale Netz und niemals
hinter einen oeffentlichen Reverse-Proxy.
