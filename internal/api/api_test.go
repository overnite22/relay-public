package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/guac"
	"github.com/overnite22/relay-public/internal/store"
	"github.com/overnite22/relay-public/internal/vault"
)

type env struct {
	t   *testing.T
	srv *httptest.Server
	st  *store.Store
}

func newEnv(t *testing.T, guacdAddr string) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	v, _ := vault.New(bytes.Repeat([]byte{1}, 32))
	a, _ := auth.New(auth.Config{Mode: "dev", DevUser: "moi@lab.fr"})
	s := New(Options{Store: st, Vault: v, Auth: a, GuacdAddr: guacdAddr, SSHHostKey: func(context.Context, string) (string, error) { return "test-key", nil }})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, st: st}
}

func (e *env) do(method, path string, body any, out any) int {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Relay", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("%s %s : %v (%s)", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func TestCSRFHeaderRequired(t *testing.T) {
	e := newEnv(t, "")
	resp, err := http.Post(e.srv.URL+"/api/folders", "application/json", strings.NewReader(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST sans X-Relay : %d", resp.StatusCode)
	}
}

func TestCredentialSecretsNeverListed(t *testing.T) {
	e := newEnv(t, "")
	var c map[string]any
	pw := "Sup3r-S3cret!"
	if code := e.do("POST", "/api/credentials", map[string]any{"name": "admin", "username": "root", "password": pw, "totp": "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"}, &c); code != 200 {
		t.Fatalf("création : %d %v", code, c)
	}
	id := c["id"].(string)
	if c["hasPassword"] != true || c["hasTotp"] != true {
		t.Fatalf("indicateurs : %v", c)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/credentials", nil)
	resp, _ := http.DefaultClient.Do(req)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), pw) || strings.Contains(string(raw), "v1:") {
		t.Fatalf("secret exposé dans la liste : %s", raw)
	}
	// En base, le mot de passe est chiffré.
	stored, _ := e.st.GetCredential(context.Background(), id)
	if stored.PasswordEnc == "" || strings.Contains(stored.PasswordEnc, pw) {
		t.Fatalf("mot de passe non chiffré en base : %q", stored.PasswordEnc)
	}
	var rev map[string]string
	if code := e.do("POST", "/api/credentials/"+id+"/reveal", map[string]string{"field": "password"}, &rev); code != 200 || rev["value"] != pw {
		t.Fatalf("révélation : %d %v", code, rev)
	}
	var otp map[string]any
	if code := e.do("GET", "/api/credentials/"+id+"/totp", nil, &otp); code != 200 || len(otp["code"].(string)) != 6 {
		t.Fatalf("TOTP : %d %v", code, otp)
	}
	// Mise à jour sans champ password : inchangé.
	if code := e.do("PUT", "/api/credentials/"+id, map[string]any{"name": "admin2", "username": "root"}, &c); code != 200 {
		t.Fatalf("mise à jour : %d", code)
	}
	e.do("POST", "/api/credentials/"+id+"/reveal", map[string]string{"field": "password"}, &rev)
	if rev["value"] != pw {
		t.Fatalf("mot de passe perdu à la mise à jour : %q", rev["value"])
	}
	// Effacement explicite.
	e.do("PUT", "/api/credentials/"+id, map[string]any{"name": "admin2", "password": ""}, &c)
	if c["hasPassword"] != false {
		t.Fatalf("mot de passe non effacé : %v", c)
	}
	var errResp map[string]string
	if code := e.do("POST", "/api/credentials", map[string]any{"name": "x", "totp": "pas du base32 !"}, &errResp); code != 400 {
		t.Fatalf("TOTP invalide accepté : %d", code)
	}
	var audit []store.AuditEvent
	e.do("GET", "/api/audit", nil, &audit)
	found := false
	for _, a := range audit {
		if a.Action == "credential.reveal" && a.User == "moi@lab.fr" && a.Detail == "password" {
			found = true
		}
	}
	if !found {
		t.Fatalf("révélation absente du journal : %+v", audit)
	}
}

func TestTreeCRUD(t *testing.T) {
	e := newEnv(t, "")
	var f store.Folder
	if code := e.do("POST", "/api/folders", map[string]any{"name": "Homelab"}, &f); code != 200 {
		t.Fatalf("%d", code)
	}
	var en store.Entry
	if code := e.do("POST", "/api/entries", map[string]any{"name": "pve1", "kind": "ssh", "host": "192.0.2.10", "folderId": f.ID}, &en); code != 200 {
		t.Fatalf("%d", code)
	}
	if en.Port != 22 || *en.FolderID != f.ID {
		t.Fatalf("%+v", en)
	}
	var bad map[string]string
	if code := e.do("POST", "/api/entries", map[string]any{"name": "x", "kind": "ssh"}, &bad); code != 400 || bad["error"] == "" {
		t.Fatalf("entrée invalide : %d %v", code, bad)
	}
	if code := e.do("POST", "/api/entries", map[string]any{"name": "x", "kind": "ssh", "host": "h", "inconnu": 1}, &bad); code != 400 {
		t.Fatalf("champ inconnu accepté : %d", code)
	}
	var tree struct {
		Folders []store.Folder `json:"folders"`
		Entries []store.Entry  `json:"entries"`
	}
	e.do("GET", "/api/tree", nil, &tree)
	if len(tree.Folders) != 1 || len(tree.Entries) != 1 {
		t.Fatalf("%+v", tree)
	}
	if code := e.do("DELETE", "/api/folders/"+f.ID, nil, nil); code != 204 {
		t.Fatalf("suppression : %d", code)
	}
	e.do("GET", "/api/tree", nil, &tree)
	if len(tree.Entries) != 0 {
		t.Fatalf("entrée restée après suppression du dossier")
	}
}

func TestBuildConfigRDP(t *testing.T) {
	e := store.Entry{Kind: "rdp", Host: "dc01", Port: 3389, Settings: json.RawMessage(`{"security":"nla","ignoreCert":false}`)}
	cfg, err := buildConfig(e, secrets{username: "admin", domain: "LAB", password: "pw"}, 1920, 1080, 96, "Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Params
	if p["security"] != "nla" || p["ignore-cert"] != "" || p["server-layout"] != "fr-fr-azerty" || p["domain"] != "LAB" || p["resize-method"] != "display-update" {
		t.Fatalf("%v", p)
	}
	cfg, _ = buildConfig(store.Entry{Kind: "rdp", Host: "h", Port: 1, Settings: json.RawMessage(`{}`)}, secrets{}, 0, 0, 0, "")
	if cfg.Params["ignore-cert"] != "" || cfg.Params["security"] != "any" {
		t.Fatalf("valeurs par défaut : %v", cfg.Params)
	}
}

// Session de bout en bout : connect → jeton → WebSocket → guacd simulé.
func TestSessionEndToEnd(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	gotConnect := make(chan []string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		read := func() guac.Instruction {
			var buf []byte
			for {
				b, err := r.ReadByte()
				if err != nil {
					return guac.Instruction{}
				}
				buf = append(buf, b)
				if n, elems, _ := guac.Split(buf); n > 0 {
					return guac.Instruction{Opcode: elems[0], Args: elems[1:]}
				}
			}
		}
		read() // select
		c.Write([]byte(guac.New("args", "VERSION_1_5_0", "hostname", "port", "username", "password", "private-key").Encode()))
		for {
			in := read()
			if in.Opcode == "connect" {
				gotConnect <- in.Args
				break
			}
			if in.Opcode == "" {
				return
			}
		}
		c.Write([]byte(guac.New("ready", "$id").Encode()))
		io.Copy(io.Discard, c)
	}()

	e := newEnv(t, ln.Addr().String())
	var c map[string]any
	e.do("POST", "/api/credentials", map[string]any{"name": "root", "username": "root", "password": "pw;é"}, &c)
	var en store.Entry
	e.do("POST", "/api/entries", map[string]any{"name": "pve1", "kind": "ssh", "host": "10.0.0.5", "credentialId": c["id"]}, &en)
	var tok map[string]string
	if code := e.do("POST", "/api/entries/"+en.ID+"/connect", map[string]any{"width": 800, "height": 600}, &tok); code != 200 {
		t.Fatalf("connect : %d %v", code, tok)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/tunnel?token=" + tok["token"]
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{Subprotocols: []string{"guacamole"}})
	if err != nil {
		t.Fatal(err)
	}
	_, msg, err := ws.Read(ctx)
	if err != nil || string(msg) != "0.,3.$id;" {
		t.Fatalf("premier message : %q %v", msg, err)
	}
	select {
	case args := <-gotConnect:
		if strings.Join(args, "|") != "VERSION_1_5_0|10.0.0.5|22|root|pw;é|" {
			t.Fatalf("connect : %q", args)
		}
	case <-ctx.Done():
		t.Fatal("pas de connect")
	}
	ws.Close(websocket.StatusNormalClosure, "")

	// Jeton à usage unique.
	ws2, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{Subprotocols: []string{"guacamole"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.CloseNow()
	_, msg, _ = ws2.Read(ctx)
	_, msg2, _ := ws2.Read(ctx)
	if !strings.Contains(string(msg)+string(msg2), "5.error") {
		t.Fatalf("jeton réutilisé accepté : %q %q", msg, msg2)
	}
}

func TestTokenBoundToUser(t *testing.T) {
	ts := newSessionTokens()
	tok := ts.put(pendingSession{user: "a@lab"})
	if _, ok := ts.take(tok, "b@lab"); ok {
		t.Fatal("jeton utilisé par un autre utilisateur")
	}
	if _, ok := ts.take(tok, "a@lab"); ok {
		t.Fatal("jeton consommé par la tentative précédente : doit rester invalide")
	}
}

// Console Proxmox : connect → vncproxy, puis tunnel WebSocket binaire relayé.
func TestProxmoxConsoleTunnel(t *testing.T) {
	pve := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=relay@pve!c=s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api2/json/nodes/pve1/qemu/105/vncproxy":
			w.Write([]byte(`{"data":{"port":"5901","ticket":"PVEVNC:abc","user":"relay@pve"}}`))
		case "/api2/json/nodes/pve1/qemu/105/vncwebsocket":
			if r.URL.Query().Get("vncticket") != "PVEVNC:abc" || r.URL.Query().Get("port") != "5901" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
			if err != nil {
				return
			}
			defer c.CloseNow()
			c.Write(r.Context(), websocket.MessageBinary, []byte("RFB 003.008\n"))
			typ, data, err := c.Read(r.Context())
			if err == nil {
				c.Write(r.Context(), typ, append([]byte("echo:"), data...))
			}
			c.Read(r.Context())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer pve.Close()

	e := newEnv(t, "")
	var src map[string]any
	if code := e.do("POST", "/api/sources", map[string]any{"kind": "proxmox", "name": "Lab", "url": pve.URL, "tokenId": "relay@pve!c", "secret": "s3cret", "verifyTls": false}, &src); code != 200 {
		t.Fatalf("source : %d %v", code, src)
	}
	ctx := context.Background()
	sid := src["id"].(string)
	en, err := e.st.SaveEntry(ctx, store.Entry{Name: "vm105", Kind: "pve", Settings: json.RawMessage(`{"sourceId":"` + sid + `","node":"pve1","vmid":105,"type":"qemu"}`), SourceID: &sid, ExternalID: "guest/105/console"})
	if err != nil {
		t.Fatal(err)
	}
	var conn map[string]string
	if code := e.do("POST", "/api/entries/"+en.ID+"/connect", map[string]any{}, &conn); code != 200 || conn["password"] != "PVEVNC:abc" || conn["protocol"] != "pve" {
		t.Fatalf("connect : %d %v", code, conn)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(cctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/api/vnc-tunnel?token="+conn["token"], &websocket.DialOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	_, b, err := ws.Read(cctx)
	if err != nil || string(b) != "RFB 003.008\n" {
		t.Fatalf("bannière RFB : %q %v", b, err)
	}
	ws.Write(cctx, websocket.MessageBinary, []byte{1, 2, 3})
	_, b, err = ws.Read(cctx)
	if err != nil || string(b) != "echo:\x01\x02\x03" {
		t.Fatalf("relais : %q %v", b, err)
	}
	// Le secret de la source n'apparaît jamais dans l'API.
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/sources", nil)
	resp, _ := http.DefaultClient.Do(req)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), "s3cret") {
		t.Fatalf("secret exposé : %s", raw)
	}
}

func TestImportRDMEndpoints(t *testing.T) {
	e := newEnv(t, "")
	xml := `<ArrayOfConnection><Connection><ConnectionType>SSHShell</ConnectionType><Name>srv</Name><Group>LXC</Group><Terminal><Host>10.0.0.9</Host></Terminal></Connection></ArrayOfConnection>`
	post := func(path string) (int, string) {
		req, _ := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(xml))
		req.Header.Set("X-Relay", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := post("/api/import/rdm/preview"); code != 200 || !strings.Contains(body, `"host":"10.0.0.9"`) {
		t.Fatalf("aperçu : %d %s", code, body)
	}
	if code, body := post("/api/import/rdm/apply?folder=Depuis%20RDM"); code != 200 || !strings.Contains(body, `"entries":1`) {
		t.Fatalf("import : %d %s", code, body)
	}
	var tree struct {
		Folders []store.Folder `json:"folders"`
	}
	e.do("GET", "/api/tree", nil, &tree)
	if len(tree.Folders) != 2 {
		t.Fatalf("dossiers : %+v", tree.Folders)
	}
}

func TestBuildConfigSSHSFTP(t *testing.T) {
	cfg, _ := buildConfig(store.Entry{Kind: "ssh", Host: "h", Port: 22, Settings: json.RawMessage(`{}`)}, secrets{}, 0, 0, 0, "")
	if cfg.Params["enable-sftp"] != "true" || cfg.Params["sftp-root-directory"] != "/" {
		t.Fatalf("SFTP par défaut : %v", cfg.Params)
	}
	cfg, _ = buildConfig(store.Entry{Kind: "ssh", Host: "h", Port: 22, Settings: json.RawMessage(`{"sftp":false}`)}, secrets{}, 0, 0, 0, "")
	if cfg.Params["enable-sftp"] != "" {
		t.Fatalf("SFTP non désactivé : %v", cfg.Params)
	}
}

func TestBuildConfigRDPDrive(t *testing.T) {
	cfg, _ := buildConfig(store.Entry{Kind: "rdp", Host: "h", Port: 3389, Settings: json.RawMessage(`{}`)}, secrets{user: "Moi@Lab.fr/../x"}, 0, 0, 0, "")
	if cfg.Params["enable-drive"] != "true" || cfg.Params["drive-path"] != "/drive/"+safeDir("moi@lab.fr/../x") || cfg.Params["drive-name"] != "Relay" {
		t.Fatalf("lecteur : %v", cfg.Params)
	}
	cfg, _ = buildConfig(store.Entry{Kind: "rdp", Host: "h", Port: 3389, Settings: json.RawMessage(`{"drive":false}`)}, secrets{}, 0, 0, 0, "")
	if cfg.Params["enable-drive"] != "" {
		t.Fatalf("lecteur non désactivé : %v", cfg.Params)
	}
}

func TestDuplicatesAndMerge(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	src, err := e.st.SaveSource(ctx, store.Source{ID: store.NewID(), Kind: "netbox", Name: "NetBox", URL: "https://nb"})
	if err != nil {
		t.Fatal(err)
	}
	cred, _ := e.st.SaveCredential(ctx, store.Credential{ID: store.NewID(), Name: "root"})
	manual, _ := e.st.SaveEntry(ctx, store.Entry{Name: "srv (RDM)", Kind: "ssh", Host: "127.0.0.1", CredentialID: &cred.ID, Notes: "note perso", Favorite: true})
	synced, _ := e.st.SaveEntry(ctx, store.Entry{Name: "srv", Kind: "ssh", Host: "localhost", SourceID: &src.ID, ExternalID: "vm/1"})
	synced2, _ := e.st.SaveEntry(ctx, store.Entry{Name: "srv bis", Kind: "ssh", Host: "127.0.0.1", SourceID: &src.ID, ExternalID: "vm/2"})
	e.st.SaveEntry(ctx, store.Entry{Name: "srv web", Kind: "web", URL: "https://127.0.0.1/"})
	e.st.SaveEntry(ctx, store.Entry{Name: "srv web 2", Kind: "web", URL: "https://localhost:443"})
	e.st.SaveEntry(ctx, store.Entry{Name: "autre port", Kind: "ssh", Host: "127.0.0.1", Port: 2222})

	var groups []DuplicateGroup
	e.do("GET", "/api/duplicates", nil, &groups)
	if len(groups) != 2 {
		t.Fatalf("groupes : %+v", groups)
	}
	var sshGroup DuplicateGroup
	for _, g := range groups {
		if strings.HasPrefix(g.Label, "SSH") {
			sshGroup = g
		}
	}
	if len(sshGroup.Entries) != 3 || sshGroup.Entries[0].SourceID == nil {
		t.Fatalf("groupe SSH (entrées synchronisées en tête attendues) : %+v", sshGroup)
	}

	// On garde l'entrée manuelle : elle adopte l'identité de vm/1, vm/2 est exclue.
	var kept store.Entry
	if code := e.do("POST", "/api/entries/"+manual.ID+"/merge", map[string]any{"ids": []string{synced.ID, synced2.ID}}, &kept); code != 200 {
		t.Fatalf("fusion : %d", code)
	}
	if kept.SourceID == nil || *kept.SourceID != src.ID || kept.ExternalID != "vm/1" || !kept.Favorite || kept.CredentialID == nil {
		t.Fatalf("entrée gardée : %+v", kept)
	}
	ex, _ := e.st.SyncExclusions(ctx, src.ID)
	if !ex["vm/2"] || ex["vm/1"] {
		t.Fatalf("exclusions : %v", ex)
	}
	if _, err := e.st.GetEntry(ctx, synced2.ID); err == nil {
		t.Fatal("doublon non supprimé")
	}
	// Suppression manuelle d'une entrée synchronisée : exclue elle aussi.
	e.do("DELETE", "/api/entries/"+kept.ID, nil, nil)
	ex, _ = e.st.SyncExclusions(ctx, src.ID)
	if !ex["vm/1"] {
		t.Fatalf("suppression non exclue : %v", ex)
	}
}
