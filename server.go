package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"strings"

	email "gopkg.in/jordan-wright/email.v1"
)

type SendHandler struct{}

type Email struct {
	From    string
	Subject string `json:'-'`
	Body    string
}

var inboxAddress string
var outboundSender string
var whitelistedDomain string

func (m *Email) ConstructMessage() ([]byte, error) {
	message := email.NewEmail()
	message.From = m.From
	message.To = []string{inboxAddress}
	message.Subject = m.Subject
	message.Text = []byte(m.Body)
	return message.Bytes()
}

func (e *Email) Send() error {
	var err error
	var servers = make([]string, 0)

	mailTokens := strings.Split(inboxAddress, "@")
	domain := mailTokens[len(mailTokens)-1]

	mxServers, err := net.LookupMX(domain)
	if err != nil {
		return err
	}
	for _, server := range mxServers {
		servers = append(servers, fmt.Sprintf("%s:25", strings.TrimRight(server.Host, ".")))
	}

	for _, server := range servers {
		msg, err := e.ConstructMessage()
		if err == nil {
			log.Printf("Attempting send to: %s, smtp_from: %s, rcpt_to: %s, message: %s\n", server, outboundSender, inboxAddress, string(msg))
			err = smtp.SendMail(
				server,
				nil,
				outboundSender,
				[]string{inboxAddress},
				msg,
			)
			if err == nil {
				break
			} else {
				log.Printf("Received error from mx server: %s\n", err.Error())
			}
		}
	}
	return err
}

func sendErrorMessage(err error) {
	log.Printf("Got Error: %s\n", err.Error())
	mailTokens := strings.Split(outboundSender, "@")
	domain := mailTokens[len(mailTokens)-1]
	from := fmt.Sprintf("errors@%s", domain)
	email := &Email{From: from, Subject: "Application Error", Body: err.Error()}
	email.Send()
}

func corsPanicHandler(h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var err error
		defer func() {
			recovery := recover()
			if recovery != nil {
				switch val := recovery.(type) {
				case string:
					err = errors.New(val)
				case error:
					err = val
				default:
					err = errors.New("Unknown error")
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}()

		if r.Method == "OPTIONS" {
			if origin := r.Header.Get("Origin"); origin == whitelistedDomain {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "POST")
				w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding")
			}
		} else {
			h.ServeHTTP(w, r)
		}
	}
}

func (s *SendHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.URL.Path != "/send" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "404")
		return
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		fmt.Fprint(w, "415")
		return
	}
	if accept := r.Header.Get("Accept"); accept != "*/*" && accept != "application/json" {
		w.WriteHeader(http.StatusNotAcceptable)
		fmt.Fprint(w, "406")
		return
	}

	decoder := json.NewDecoder(r.Body)
	var message Email
	err := decoder.Decode(&message)
	if err != nil {
		w.WriteHeader(http.StatusNotAcceptable)
		fmt.Fprintf(w, "422")
		return
	}

	go func() {
		message.Subject = "New Web Inquiry"
		message.Send()
	}()

	w.WriteHeader(http.StatusAccepted)
	return
}

func main() {
	inboxAddress = os.Getenv("MAILER_INBOX")
	outboundSender = os.Getenv("MAILER_SENDER")
	whitelistedDomain = os.Getenv("MAILER_WHITELISTED_DOMAIN")
	mailerPort := os.Getenv("MAILER_PORT")
	if inboxAddress == "" || outboundSender == "" || whitelistedDomain == "" {
		log.Fatal("MAILER_INBOX, MAILER_SENDER, and MAILER_WHITELISTED_DOMAIN must be set")
		os.Exit(1)
	}
	if mailerPort == "" {
		mailerPort = "8080"
	}
	sendEndpoint := &SendHandler{}
	http.Handle("/send", corsPanicHandler(sendEndpoint))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", mailerPort), nil))
}
