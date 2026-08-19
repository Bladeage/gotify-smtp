package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/emersion/go-smtp"
	"github.com/gotify/plugin-api"
)

// fakeHandler faengt die Nachricht ab, die das Plugin an Gotify schicken wuerde.
type fakeHandler struct {
	nachrichten []plugin.Message
}

func (f *fakeHandler) SendMessage(msg plugin.Message) error {
	f.nachrichten = append(f.nachrichten, msg)
	return nil
}

// neuesPlugin liefert eine Instanz mit den Standardwerten, ohne den SMTP-Server
// zu starten -- ValidateAndSetConfig wuerde einen Port belegen.
func neuesPlugin(t *testing.T, config *Config) (*Plugin, *fakeHandler) {
	t.Helper()

	p := &Plugin{userCtx: plugin.UserContext{ID: 1, Name: "TestUser"}}

	if config == nil {
		config = p.DefaultConfig().(*Config)
	}

	p.configLock.Lock()
	p.config = config
	p.configLock.Unlock()

	h := &fakeHandler{}
	p.SetMessageHandler(h)

	return p, h
}

func TestDefaultConfig(t *testing.T) {
	p, _ := neuesPlugin(t, nil)
	config := p.currentConfig()

	if !config.StripHTML {
		t.Error("strip_html sollte standardmaessig an sein")
	}

	if config.StripSubjectPrefix {
		t.Error("strip_subject_prefix sollte standardmaessig aus sein")
	}

	if config.ListenAddr == "" {
		t.Error("listen_addr fehlt")
	}
}

func TestValidateAndSetConfigLehntUngueltigesAb(t *testing.T) {
	p, _ := neuesPlugin(t, nil)

	faelle := map[string]*Config{
		"Prioritaet zu hoch":     {Priority: 11, ListenAddr: ":1025"},
		"Prioritaet negativ":     {Priority: -1, ListenAddr: ":1025"},
		"leerer Absendereintrag": {AllowedSenders: []string{" "}, ListenAddr: ":1025"},
		"kaputte Adresse":        {ListenAddr: "keinport"},
	}

	for name, config := range faelle {
		if err := p.ValidateAndSetConfig(config); err == nil {
			t.Errorf("%s haette abgelehnt werden muessen", name)
		}
	}
}

func TestStripPrefix(t *testing.T) {
	faelle := map[string]string{
		"[TestNAS] Test Message": "Test Message",
		"[TestNAS]  Doppelt ":    "Doppelt",
		"Ohne Klammer":           "Ohne Klammer",
		"[TestNAS]":              "[TestNAS]", // nichts uebrig -> unveraendert
		"[unvollstaendig":        "[unvollstaendig",
	}

	for eingabe, erwartet := range faelle {
		if got := stripPrefix(eingabe); got != erwartet {
			t.Errorf("stripPrefix(%q) = %q, erwartet %q", eingabe, got, erwartet)
		}
	}
}

func TestPasswortpruefung(t *testing.T) {
	p, _ := neuesPlugin(t, &Config{Password: "geheim", ListenAddr: ":1025"})

	userLock.Lock()
	users["TestUser"] = p
	userLock.Unlock()

	defer func() {
		userLock.Lock()
		delete(users, "TestUser")
		userLock.Unlock()
	}()

	bkd := &Backend{}

	if _, err := bkd.Login(&smtp.ConnectionState{}, "TestUser", "falsch"); err == nil {
		t.Error("falsches Passwort haette abgelehnt werden muessen")
	}

	if _, err := bkd.Login(&smtp.ConnectionState{}, "TestUser", "geheim"); err != nil {
		t.Errorf("richtiges Passwort abgelehnt: %v", err)
	}

	if _, err := bkd.Login(&smtp.ConnectionState{}, "Unbekannt", "geheim"); err == nil {
		t.Error("unbekannter Benutzer haette abgelehnt werden muessen")
	}
}

func TestAbsenderfilter(t *testing.T) {
	p, _ := neuesPlugin(t, &Config{AllowedSenders: []string{"@example.com", "nas@intern.test"}})
	s := &Session{c: p}

	erlaubt := []string{"beliebig@example.com", "NAS@Intern.Test"}
	verboten := []string{"fremd@anderswo.test", ""}

	for _, from := range erlaubt {
		if err := s.Mail(from, smtp.MailOptions{}); err != nil {
			t.Errorf("%q haette erlaubt sein muessen: %v", from, err)
		}
	}

	for _, from := range verboten {
		if err := s.Mail(from, smtp.MailOptions{}); err == nil {
			t.Errorf("%q haette abgelehnt werden muessen", from)
		}
	}
}

// Der komplette Weg: echte QNAP-Mail durch Session.Data bis zur Gotify-Nachricht.
func TestDataMitEchterQnapMail(t *testing.T) {
	roh, err := os.ReadFile("testdata/qnap-test-message.eml")

	if err != nil {
		t.Skipf("Mitschnitt nicht vorhanden: %v", err)
	}

	p, h := neuesPlugin(t, &Config{Priority: 8, StripHTML: true, StripSubjectPrefix: true})

	if err := (&Session{c: p}).Data(bytes.NewReader(roh)); err != nil {
		t.Fatalf("Data() = %v", err)
	}

	if len(h.nachrichten) != 1 {
		t.Fatalf("%d Nachrichten erzeugt, erwartet 1", len(h.nachrichten))
	}

	msg := h.nachrichten[0]

	if msg.Priority != 8 {
		t.Errorf("Prioritaet = %d, erwartet 8", msg.Priority)
	}

	if msg.Title != "Test Message" {
		t.Errorf("Titel = %q, erwartet \"Test Message\" (Praefix entfernt)", msg.Title)
	}

	if strings.HasPrefix(msg.Message, "[TestNAS] Test Message") {
		t.Errorf("Betreff im Text wiederholt: %q", msg.Message)
	}

	if !strings.HasPrefix(msg.Message, "NAS Name: TestNAS") {
		t.Errorf("Text beginnt unerwartet: %q", msg.Message)
	}
}

func TestDataOhneOptionen(t *testing.T) {
	roh, err := os.ReadFile("testdata/qnap-test-message.eml")

	if err != nil {
		t.Skipf("Mitschnitt nicht vorhanden: %v", err)
	}

	p, h := neuesPlugin(t, &Config{StripHTML: true})

	if err := (&Session{c: p}).Data(bytes.NewReader(roh)); err != nil {
		t.Fatalf("Data() = %v", err)
	}

	if h.nachrichten[0].Title != "[TestNAS] Test Message" {
		t.Errorf("Titel = %q", h.nachrichten[0].Title)
	}
}

// Mit strip_html=false soll das Markup unveraendert durchgereicht werden.
func TestDataOhneHtmlAbbau(t *testing.T) {
	roh, err := os.ReadFile("testdata/qnap-test-message.eml")

	if err != nil {
		t.Skipf("Mitschnitt nicht vorhanden: %v", err)
	}

	p, h := neuesPlugin(t, &Config{StripHTML: false})

	if err := (&Session{c: p}).Data(bytes.NewReader(roh)); err != nil {
		t.Fatalf("Data() = %v", err)
	}

	if !strings.Contains(h.nachrichten[0].Message, "<html") {
		t.Errorf("Markup wurde trotz strip_html=false abgebaut: %.80q", h.nachrichten[0].Message)
	}
}

// Ohne Betreff greift der konfigurierte Ersatztitel.
func TestDefaultTitle(t *testing.T) {
	p, h := neuesPlugin(t, &Config{DefaultTitle: "(kein Betreff)", StripHTML: true})

	mail := "Content-Type: text/plain; charset=utf-8\r\n\r\nNur Text\r\n"

	if err := (&Session{c: p}).Data(strings.NewReader(mail)); err != nil {
		t.Fatalf("Data() = %v", err)
	}

	if h.nachrichten[0].Title != "(kein Betreff)" {
		t.Errorf("Titel = %q", h.nachrichten[0].Title)
	}
}
