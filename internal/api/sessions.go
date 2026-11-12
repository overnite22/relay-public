package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/overnite22/relay-public/internal/guac"
	"github.com/overnite22/relay-public/internal/store"
)

// pendingSession est une session préparée, en attente de l'ouverture du
// WebSocket. Le jeton est à usage unique et expire vite : les secrets
// déchiffrés ne restent en mémoire que le temps de l'ouverture.
type pendingSession struct {
	cfg     guac.Config
	user    string
	entryID string
	name    string
	record  bool
	exp     time.Time
}

type sessionTokens struct {
	mu sync.Mutex
	m  map[string]pendingSession
}

func newSessionTokens() *sessionTokens { return &sessionTokens{m: map[string]pendingSession{}} }

const tokenTTL = 60 * time.Second

func (t *sessionTokens) put(p pendingSession) string {
	tok := store.NewID() + store.NewID()
	t.mu.Lock()
	defer t.mu.Unlock()
	p.exp = time.Now().Add(tokenTTL)
	t.m[tok] = p
	time.AfterFunc(tokenTTL, func() {
		t.mu.Lock()
		delete(t.m, tok)
		t.mu.Unlock()
	})
	return tok
}

// take consomme un jeton (usage unique) s'il est valide et appartient à user.
func (t *sessionTokens) take(tok, user string) (pendingSession, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.m[tok]
	if !ok {
		return p, false
	}
	delete(t.m, tok)
	if time.Now().After(p.exp) || p.user != user {
		return pendingSession{}, false
	}
	return p, true
}

// Paramètres avancés par type d'entrée (colonne settings, JSON).
type rdpSettings struct {
	Security       string `json:"security"`       // any, nla, tls, rdp, vmconnect
	IgnoreCert     *bool  `json:"ignoreCert"`     // défaut : false (validation du certificat)
	KeyboardLayout string `json:"keyboardLayout"` // défaut : fr-fr-azerty
	Console        bool   `json:"console"`        // session d'administration
	DisableAudio   bool   `json:"disableAudio"`
	Wallpaper      bool   `json:"wallpaper"`
	DisableCopy    bool   `json:"disableCopy"`
	DisablePaste   bool   `json:"disablePaste"`
	Drive          *bool  `json:"drive"` // lecteur « Relay » pour transférer des fichiers (défaut : oui)
}

type sshSettings struct {
	FontName    string `json:"fontName"`
	FontSize    int    `json:"fontSize"`
	ColorScheme string `json:"colorScheme"`
	Scrollback  int    `json:"scrollback"`
	SFTP        *bool  `json:"sftp"`     // transfert de fichiers (défaut : oui)
	SFTPRoot    string `json:"sftpRoot"` // dossier racine du navigateur de fichiers (défaut : /)
}

type vncSettings struct {
	ColorDepth int    `json:"colorDepth"`
	ReadOnly   bool   `json:"readOnly"`
	Cursor     string `json:"cursor"` // local | remote
}

// secrets regroupe les secrets déchiffrés utiles à une session.
type secrets struct {
	username, domain, password, privateKey, passphrase string
	user                                               string // utilisateur Relay (dossier du lecteur RDP)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return ""
}

// buildConfig traduit une entrée en paramètres guacd.
func buildConfig(e store.Entry, s secrets, width, height, dpi int, tz string) (guac.Config, error) {
	p := map[string]string{
		"hostname": e.Host,
		"port":     strconv.Itoa(e.Port),
		"username": s.username,
		"password": s.password,
	}
	cfg := guac.Config{Protocol: e.Kind, Params: p, Width: width, Height: height, DPI: dpi, Timezone: tz}
	switch e.Kind {
	case store.KindRDP:
		var st rdpSettings
		if err := json.Unmarshal(e.Settings, &st); err != nil {
			return cfg, err
		}
		p["domain"] = s.domain
		p["security"] = orDefault(st.Security, "any")
		if st.IgnoreCert != nil && *st.IgnoreCert {
			p["ignore-cert"] = "true"
		}
		p["server-layout"] = orDefault(st.KeyboardLayout, "fr-fr-azerty")
		p["resize-method"] = "display-update"
		p["console"] = boolStr(st.Console)
		p["disable-audio"] = boolStr(st.DisableAudio)
		p["enable-wallpaper"] = boolStr(st.Wallpaper)
		p["enable-font-smoothing"] = "true"
		p["disable-copy"] = boolStr(st.DisableCopy)
		p["disable-paste"] = boolStr(st.DisablePaste)
		if st.Drive == nil || *st.Drive {
			p["enable-drive"] = "true"
			p["drive-name"] = "Relay"
			p["drive-path"] = driveRoot + "/" + safeDir(s.user)
			p["create-drive-path"] = "true"
		}
		if tz != "" {
			p["timezone"] = tz
		}
		cfg.AudioTypes = []string{"audio/L16"}
	case store.KindSSH:
		var st sshSettings
		if err := json.Unmarshal(e.Settings, &st); err != nil {
			return cfg, err
		}
		p["private-key"] = s.privateKey
		p["passphrase"] = s.passphrase
		p["font-name"] = orDefault(st.FontName, "monospace")
		size := st.FontSize
		if size < 6 || size > 48 {
			size = 12
		}
		p["font-size"] = strconv.Itoa(size)
		p["color-scheme"] = orDefault(st.ColorScheme, "gray-black")
		if st.Scrollback > 0 {
			p["scrollback"] = strconv.Itoa(st.Scrollback)
		}
		p["terminal-type"] = "xterm-256color"
		p["server-alive-interval"] = "30"
		if st.SFTP == nil || *st.SFTP {
			p["enable-sftp"] = "true"
			p["sftp-root-directory"] = orDefault(st.SFTPRoot, "/")
		}
		if tz != "" {
			p["timezone"] = tz
		}
	case store.KindVNC:
		var st vncSettings
		if err := json.Unmarshal(e.Settings, &st); err != nil {
			return cfg, err
		}
		if st.ColorDepth == 8 || st.ColorDepth == 16 || st.ColorDepth == 24 || st.ColorDepth == 32 {
			p["color-depth"] = strconv.Itoa(st.ColorDepth)
		}
		p["read-only"] = boolStr(st.ReadOnly)
		if st.Cursor == "remote" {
			p["cursor"] = "remote"
		}
	}
	return cfg, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// driveRoot est le dossier (dans le conteneur guacd) qui contient un
// sous-dossier par utilisateur pour le lecteur RDP « Relay ».
const driveRoot = "/drive"

// safeDir transforme un identifiant d'utilisateur en nom de dossier sûr.
func safeDir(user string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(user))))
	return hex.EncodeToString(sum[:])
}
