package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/overnite22/relay-public/internal/store"
)

func (s *Server) quickConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		sessionOptions
		Kind         string  `json:"kind"`
		Host         string  `json:"host"`
		Port         int     `json:"port"`
		Username     string  `json:"username"`
		Domain       string  `json:"domain"`
		Password     string  `json:"password"`
		CredentialID *string `json:"credentialId"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Kind != store.KindSSH && in.Kind != store.KindRDP {
		s.fail(w, r, store.ErrInvalid{Msg: "choisissez SSH ou RDP"})
		return
	}
	host := strings.TrimSpace(in.Host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") && net.ParseIP(host[1:len(host)-1]) != nil {
		host = host[1 : len(host)-1]
	}
	if host == "" || len(host) > 253 || (net.ParseIP(host) == nil && strings.IndexFunc(host, invalidHostRune) >= 0) {
		s.fail(w, r, store.ErrInvalid{Msg: "saisissez une adresse IP ou un nom d’hôte, avec le port dans le champ dédié"})
		return
	}
	if in.Port == 0 {
		in.Port = store.DefaultPort(in.Kind)
	}
	if in.Port < 1 || in.Port > 65535 {
		s.fail(w, r, store.ErrInvalid{Msg: "le port doit être compris entre 1 et 65535"})
		return
	}
	if in.CredentialID != nil && in.Password != "" {
		s.fail(w, r, store.ErrInvalid{Msg: "choisissez un identifiant du coffre ou saisissez un mot de passe"})
		return
	}
	entry := store.Entry{
		Kind: in.Kind, Name: net.JoinHostPort(host, strconv.Itoa(in.Port)),
		Host: host, Port: in.Port, Username: strings.TrimSpace(in.Username),
		CredentialID: in.CredentialID, Settings: json.RawMessage(`{}`),
	}
	sec, err := s.sessionSecrets(r.Context(), entry)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if in.CredentialID == nil {
		sec.password = in.Password
		if in.Kind == store.KindRDP {
			sec.domain = strings.TrimSpace(in.Domain)
		}
	}
	s.prepareGuacSession(w, r, entry, sec, in.sessionOptions)
}

func invalidHostRune(r rune) bool {
	return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_')
}
