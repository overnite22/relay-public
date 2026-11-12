// Package rdmimport lit un export Remote Desktop Manager (.rdm, XML
// « ArrayOfConnection ») et le traduit en dossiers, entrées et identifiants
// Relay.
//
// La lecture est volontairement tolérante : chaque <Connection> est parcourue
// comme un arbre XML générique et les champs utiles sont cherchés à plusieurs
// endroits possibles (racine de l'entrée, sous-blocs Terminal, RDP, VNC,
// Credentials...). Les mots de passe chiffrés par RDM (SafePassword) ne
// peuvent pas être relus : ils sont signalés pour être ressaisis dans le coffre.
package rdmimport

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/overnite22/relay-public/internal/store"
)

// node est un élément XML générique.
type node struct {
	Name       string
	Text       string
	textBuffer strings.Builder
	Children   []*node
}

func (n *node) child(name string) *node {
	for _, c := range n.Children {
		if strings.EqualFold(c.Name, name) {
			return c
		}
	}
	return nil
}

func (n *node) text(name string) string {
	if c := n.child(name); c != nil {
		return strings.TrimSpace(c.Text)
	}
	return ""
}

// find cherche en profondeur le premier élément nommé parmi names, dans les
// sous-blocs (hors racine), en ignorant les blocs sans rapport.
func (n *node) find(names ...string) string {
	for _, c := range n.Children {
		for _, name := range names {
			if strings.EqualFold(c.Name, name) && strings.TrimSpace(c.Text) != "" && len(c.Children) == 0 {
				return strings.TrimSpace(c.Text)
			}
		}
	}
	for _, c := range n.Children {
		if len(c.Children) == 0 || skipBlock[strings.ToLower(c.Name)] {
			continue
		}
		if v := c.find(names...); v != "" {
			return v
		}
	}
	return ""
}

// Blocs qui contiennent des champs homonymes sans rapport avec la cible.
var skipBlock = map[string]bool{
	"metainformation": true, "events": true, "subconnections": true, "attachments": true,
	"customfields": true, "documentation": true, "vpn": true, "gateway": true, "sshgateway": true,
	"proxy": true, "beforeopen": true, "afteropen": true, "beforeclose": true, "afterclose": true,
}

func parseXML(r io.Reader) (*node, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	var stack []*node
	var root *node
	nodes := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML invalide : %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			nodes++
			if nodes > 100000 {
				return nil, errors.New("export trop complexe (100000 éléments maximum)")
			}
			n := &node{Name: t.Name.Local}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("XML invalide : plusieurs racines")
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			}
			stack = append(stack, n)
			if len(stack) > 64 {
				return nil, errors.New("XML trop profond")
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack[len(stack)-1].Text = stack[len(stack)-1].textBuffer.String()
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].textBuffer.Write(t)
			}
		}
	}
	if root == nil {
		return nil, errors.New("fichier vide")
	}
	return root, nil
}

// Folder, Entry, Credential, Skipped : contenu du plan d'import.
type Folder struct {
	Path []string `json:"path"`
}

type Entry struct {
	SourceID      string   `json:"-"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	URL           string   `json:"url"`
	Username      string   `json:"username"`
	Path          []string `json:"path"`
	CredentialRef string   `json:"-"`
	Credential    string   `json:"credential"` // nom de l'identifiant lié
	Notes         string   `json:"-"`
	InlineSecret  bool     `json:"inlineSecret"` // mot de passe propre à l'entrée, non relisible
	RDMType       string   `json:"rdmType"`
}

type Credential struct {
	SourceID        string `json:"-"`
	Name            string `json:"name"`
	Username        string `json:"username"`
	Domain          string `json:"domain"`
	Password        string `json:"-"`
	HasClear        bool   `json:"hasPassword"`
	EncryptedSecret bool   `json:"encryptedSecret"`
	PrivateKey      string `json:"-"`
	HasPrivateKey   bool   `json:"hasPrivateKey"`
}

type Skipped struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// Plan est le résultat de l'analyse d'un export.
type Plan struct {
	Folders     []Folder     `json:"folders"`
	Entries     []Entry      `json:"entries"`
	Credentials []Credential `json:"credentials"`
	Skipped     []Skipped    `json:"skipped"`
	Warnings    []string     `json:"warnings"`
}

var kindByType = map[string]string{
	"rdpconfigured": store.KindRDP, "rdpfilename": store.KindRDP,
	"sshshell": store.KindSSH, "putty": store.KindSSH, "terminalconsole": store.KindSSH, "unixterminal": store.KindSSH,
	"vnc": store.KindVNC, "appleremotedesktop": store.KindVNC,
	"webbrowser": store.KindWeb, "idrac": store.KindWeb, "ilo": store.KindWeb, "dellremoteaccesscontroller": store.KindWeb,
	"hpintegratedlightsout": store.KindWeb,
}

var defaultPorts = map[string]int{store.KindRDP: 3389, store.KindSSH: 22, store.KindVNC: 5900}

func splitPath(g string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(g, "/", "\\"), "\\") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitHostPort sépare "hôte:port" (IPv6 entre crochets acceptée).
func splitHostPort(s string) (string, int) {
	s = strings.TrimSpace(s)
	if h, p, err := net.SplitHostPort(s); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return h, n
		}
	}
	return strings.Trim(s, "[]"), 0
}

// specialCredentialRefs sont des valeurs réservées de RDM pour
// CredentialConnectionID (aucun identifiant, hériter du dossier parent,
// identifiants personnels...). Elles ne désignent pas une entrée de l'export.
var specialCredentialRefs = map[string]bool{
	"245a4245-48e7-4df5-9c4c-11861d8e1f81": true, // hériter du dossier parent
	"b87b29d9-9239-4d7b-86d8-9b53dcd3ba9f": true, // aucun (valeur des dossiers)
	"0c0c8d0a-ce6d-40e7-84d0-343d488e2dba": true,
	"9f3c3bcf-068a-4927-b996-ca52154cae3b": true,
	"1310cf82-6fab-4b7a-9eea-3e2e451ca2cf": true,
	"e2cc9029-ca3a-4308-ba54-16d5029bc8ed": true,
}

func isSpecialRef(ref string) bool {
	if specialCredentialRefs[ref] {
		return true
	}
	// Préfixes connus (la fin des GUID n'est pas toujours visible dans les binaires).
	for _, p := range []string{"245a4245-", "b87b29d9-", "0c0c8d0a-", "9f3c3bcf-", "88e4be76-", "1310cf82-", "e2cc9029-"} {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return false
}

// hostInfo est une fiche « Hôte » de RDM, référencée par ses sessions.
type hostInfo struct{ host, user string }

// Parse analyse un export .rdm.
func Parse(r io.Reader) (Plan, error) {
	p := Plan{Folders: []Folder{}, Entries: []Entry{}, Credentials: []Credential{}, Skipped: []Skipped{}, Warnings: []string{}}
	root, err := parseXML(io.LimitReader(r, 64<<20))
	if err != nil {
		return p, err
	}
	conns := root.Children
	if !strings.EqualFold(root.Name, "ArrayOfConnection") {
		// Certains exports enveloppent la liste : <RDMExport><Connections>...</Connections></RDMExport>.
		if c := findList(root); c != nil {
			conns = c.Children
		} else {
			return p, errors.New("format non reconnu : un export Remote Desktop Manager (.rdm) contient une liste <ArrayOfConnection>")
		}
	}
	if len(conns) > 10000 {
		return p, errors.New("export trop volumineux (10000 entrées maximum)")
	}
	folderSeen := map[string]bool{}
	var planErr error
	addFolder := func(path []string) {
		for i := 1; i <= len(path); i++ {
			k := strings.Join(path[:i], "\x00")
			if !folderSeen[k] {
				if len(folderSeen) >= 10000 {
					planErr = errors.New("trop de dossiers dans l’export (10000 maximum)")
					return
				}
				folderSeen[k] = true
				p.Folders = append(p.Folders, Folder{Path: append([]string(nil), path[:i]...)})
			}
		}
	}
	// 1re passe : fiches « Hôte », dont les sessions héritent l'adresse.
	hosts := map[string]hostInfo{}
	for _, c := range conns {
		if strings.EqualFold(c.Name, "Connection") && strings.EqualFold(c.text("ConnectionType"), "Host") {
			blk := c.child("HostDetails")
			if blk == nil {
				blk = c
			}
			hosts[strings.ToLower(c.text("ID"))] = hostInfo{host: blk.find("Host"), user: blk.find("UserName", "Username")}
		}
	}

	credByID := map[string]int{}
	var pending []Entry
	inlineSecrets := 0

	for _, c := range conns {
		if !strings.EqualFold(c.Name, "Connection") {
			continue
		}
		typ := c.text("ConnectionType")
		lt := strings.ToLower(typ)
		name := c.text("Name")
		id := c.text("ID")
		group := c.text("Group")
		if len(group) > 4096 {
			return p, errors.New("chemin de dossier trop long (4096 octets maximum)")
		}
		path := splitPath(group)
		if len(path) > 32 {
			return p, errors.New("chemin de dossier trop profond (32 niveaux maximum)")
		}
		if path == nil {
			path = []string{}
		}
		notes := c.text("Description")
		if name == "" {
			name = "(sans nom)"
		}
		switch lt {
		case "group", "templategroup":
			// Dans RDM, le chemin d'un dossier inclut son propre nom.
			if len(path) == 0 {
				path = []string{name}
			}
			addFolder(path)
			if planErr != nil {
				return p, planErr
			}
			continue
		case "credential":
			cr := Credential{SourceID: id, Name: name}
			blk := c.child("Credentials")
			if blk == nil {
				blk = c
			}
			cr.Username = firstNonEmpty(blk.find("UserName", "Username", "PrivateKeyOverrideUsername"), c.text("UserName"))
			cr.Domain = firstNonEmpty(blk.find("Domain"), c.text("Domain"))
			cr.Password = firstNonEmpty(blk.find("Password"), c.text("Password"))
			cr.HasClear = cr.Password != ""
			if k := blk.find("PrivateKeyData"); strings.Contains(k, "PRIVATE KEY") {
				cr.PrivateKey, cr.HasPrivateKey = k, true
			}
			cr.EncryptedSecret = (!cr.HasClear && firstNonEmpty(blk.find("SafePassword", "SafePasswordAndPassphrase"), c.text("SafePassword")) != "") ||
				(!cr.HasPrivateKey && blk.find("SafePrivateKeyData") != "")
			credByID[strings.ToLower(id)] = len(p.Credentials)
			p.Credentials = append(p.Credentials, cr)
			continue
		case "host":
			p.Skipped = append(p.Skipped, Skipped{Name: name, Type: typ, Reason: "fiche hôte RDM : ses sessions sont importées avec son adresse"})
			continue
		case "proxmoxconsole":
			p.Skipped = append(p.Skipped, Skipped{Name: name, Type: typ, Reason: "utilisez la synchronisation Proxmox de Relay"})
			continue
		}
		kind, ok := kindByType[lt]
		if !ok {
			p.Skipped = append(p.Skipped, Skipped{Name: name, Type: typ, Reason: "type non pris en charge par Relay"})
			continue
		}
		e := Entry{SourceID: id, Name: name, Kind: kind, Path: path, Notes: notes, RDMType: typ,
			CredentialRef: strings.ToLower(c.text("CredentialConnectionID"))}
		hi := hosts[strings.ToLower(c.text("HostConnectionID"))]
		switch kind {
		case store.KindWeb:
			e.URL = firstNonEmpty(c.find("WebBrowserUrl"), c.text("Url"), c.find("Url", "Host"), hi.host)
			if e.URL != "" && !strings.Contains(e.URL, "://") {
				e.URL = "https://" + e.URL
			}
		default:
			raw := firstNonEmpty(c.text("Url"), c.find("Host", "HostName", "Url", "Server"), hi.host)
			e.Host, e.Port = splitHostPort(raw)
			if e.Port == 0 {
				if v, err := strconv.Atoi(firstNonEmpty(c.find("HostPort", "Port"), c.text("Port"))); err == nil {
					e.Port = v
				}
			}
			if e.Port == 0 {
				e.Port = defaultPorts[kind]
			}
		}
		e.Username = firstNonEmpty(c.text("UserName"), c.find("UserName", "Username"), hi.user)
		e.InlineSecret = firstNonEmpty(c.text("SafePassword"), c.find("SafePassword", "SafePrivateKeyData")) != ""
		if (kind != store.KindWeb && e.Host == "") || (kind == store.KindWeb && e.URL == "") {
			p.Skipped = append(p.Skipped, Skipped{Name: name, Type: typ, Reason: "hôte ou URL introuvable"})
			continue
		}
		addFolder(path)
		if planErr != nil {
			return p, planErr
		}
		pending = append(pending, e)
	}
	unresolved := 0
	for _, e := range pending {
		switch {
		case e.CredentialRef == "" || isSpecialRef(e.CredentialRef):
			e.CredentialRef = ""
		default:
			if i, ok := credByID[e.CredentialRef]; ok {
				e.Credential = p.Credentials[i].Name
			} else {
				unresolved++
				e.CredentialRef = ""
			}
		}
		// Un secret propre n'est utile que si aucun identifiant n'est lié.
		e.InlineSecret = e.InlineSecret && e.Credential == ""
		if e.InlineSecret {
			inlineSecrets++
		}
		p.Entries = append(p.Entries, e)
	}
	enc := 0
	for _, c := range p.Credentials {
		if c.EncryptedSecret {
			enc++
		}
	}
	if enc > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d identifiant(s) ont un mot de passe ou une clé chiffrés par RDM : ils seront créés sans secret, à ressaisir dans le coffre.", enc))
	}
	if inlineSecrets > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d entrée(s) ont un mot de passe ou une clé propres (non liés à un identifiant) : ils ne sont pas importés. Choisissez ci-dessous un identifiant par défaut si besoin.", inlineSecrets))
	}
	if unresolved > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d entrée(s) utilisent un identifiant absent de l'export (autre coffre) : elles seront importées sans identifiant.", unresolved))
	}
	return p, nil
}

func findList(n *node) *node {
	for _, c := range n.Children {
		if len(c.Children) > 0 && strings.EqualFold(c.Children[0].Name, "Connection") {
			return c
		}
		if f := findList(c); f != nil {
			return f
		}
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// Sealer chiffre un secret (fourni par la couche API, liée au coffre).
type Sealer func(credID, field, value string) (string, error)

// Result résume un import.
type Result struct {
	Folders     int    `json:"folders"`
	Entries     int    `json:"entries"`
	Credentials int    `json:"credentials"`
	RootID      string `json:"rootId"`
}

// Defaults désigne, par leur nom dans le plan, les identifiants à lier aux
// entrées SSH et RDP importées sans identifiant.
type Defaults struct {
	SSH string
	RDP string
}

// Apply crée le contenu du plan sous un nouveau dossier racine rootName.
func Apply(ctx context.Context, st *store.Store, p Plan, rootName string, def Defaults, seal Sealer) (Result, error) {
	var res Result
	if strings.TrimSpace(rootName) == "" {
		rootName = "Import RDM"
	}
	root, err := st.SaveFolder(ctx, store.Folder{Name: rootName})
	if err != nil {
		return res, err
	}
	res.RootID = root.ID
	folders := map[string]string{"": root.ID}
	ensure := func(path []string) (string, error) {
		parent := root.ID
		key := ""
		for _, part := range path {
			key += "\x00" + part
			if id, ok := folders[key]; ok {
				parent = id
				continue
			}
			pp := parent
			f, err := st.SaveFolder(ctx, store.Folder{Name: part, ParentID: &pp})
			if err != nil {
				return "", err
			}
			folders[key] = f.ID
			parent = f.ID
			res.Folders++
		}
		return parent, nil
	}
	for _, f := range p.Folders {
		if _, err := ensure(f.Path); err != nil {
			return res, err
		}
	}
	credIDs := map[string]string{}
	for _, c := range p.Credentials {
		id := store.NewID()
		cr := store.Credential{ID: id, Name: c.Name, Username: c.Username, Domain: c.Domain}
		if c.EncryptedSecret {
			cr.Notes = "Importé depuis RDM : mot de passe à ressaisir."
		}
		if cr.PasswordEnc, err = seal(id, "password", c.Password); err != nil {
			return res, err
		}
		if cr.PrivateKeyEnc, err = seal(id, "privateKey", c.PrivateKey); err != nil {
			return res, err
		}
		if _, err := st.SaveCredential(ctx, cr); err != nil {
			return res, fmt.Errorf("identifiant %s : %w", c.Name, err)
		}
		credIDs[strings.ToLower(c.SourceID)] = id
		if c.Name == def.SSH {
			credIDs["\x00ssh"] = id
		}
		if c.Name == def.RDP {
			credIDs["\x00rdp"] = id
		}
		res.Credentials++
	}
	for _, e := range p.Entries {
		folderID, err := ensure(e.Path)
		if err != nil {
			return res, err
		}
		en := store.Entry{FolderID: &folderID, Name: e.Name, Kind: e.Kind, Host: e.Host, Port: e.Port, URL: e.URL,
			Username: e.Username, Notes: e.Notes}
		if id, ok := credIDs[e.CredentialRef]; ok && e.CredentialRef != "" {
			en.CredentialID = &id
		} else if id, ok := credIDs["\x00"+e.Kind]; ok {
			en.CredentialID = &id
		}
		if _, err := st.SaveEntry(ctx, en); err != nil {
			return res, fmt.Errorf("entrée %s : %w", e.Name, err)
		}
		res.Entries++
	}
	return res, nil
}
