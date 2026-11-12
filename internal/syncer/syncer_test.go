package syncer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overnite22/relay-public/internal/netbox"
	"github.com/overnite22/relay-public/internal/proxmox"
	"github.com/overnite22/relay-public/internal/store"
)

func fakePVE(t *testing.T, guests string) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=relay@pve!sync=secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		data := map[string]string{
			"/api2/json/cluster/resources":                                `[{"type":"node","node":"pve1","status":"online"}` + guests + `]`,
			"/api2/json/cluster/status":                                   `[{"type":"cluster","name":"lab"},{"type":"node","name":"pve1","ip":"192.0.2.10"}]`,
			"/api2/json/nodes/pve1/lxc/101/interfaces":                    `[{"name":"lo","inet":"127.0.0.1/8"},{"name":"eth0","inet":"192.0.2.61/24"}]`,
			"/api2/json/nodes/pve1/qemu/200/config":                       `{"ostype":"win11"}`,
			"/api2/json/nodes/pve1/qemu/200/agent/network-get-interfaces": `{"result":[{"name":"Ethernet","ip-addresses":[{"ip-address-type":"ipv6","ip-address":"fe80::1"},{"ip-address-type":"ipv4","ip-address":"192.0.2.40"}]}]}`,
		}[r.URL.Path]
		if data == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"data":` + data + `}`))
	}))
}

func open(t *testing.T) *store.Store {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestProxmoxSyncLifecycle(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	cred, _ := st.SaveCredential(ctx, store.Credential{ID: store.NewID(), Name: "root"})
	src, err := st.SaveSource(ctx, store.Source{ID: store.NewID(), Kind: store.SourceProxmox, Name: "Cluster lab", URL: "https://x", TokenID: "relay@pve!sync"})
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{SSHCredentialID: cred.ID}

	guests := `,{"type":"lxc","node":"pve1","vmid":101,"name":"ct-ansible","status":"running"},{"type":"qemu","node":"pve1","vmid":200,"name":"win-admin","status":"running"},{"type":"qemu","node":"pve1","vmid":300,"name":"tpl","template":1},{"type":"qemu","node":"pve1","vmid":301,"name":"stopped","status":"stopped"}`
	srv := fakePVE(t, guests)
	defer srv.Close()
	c := proxmox.New(srv.URL, "relay@pve!sync", "secret", false)
	plan, err := CollectProxmox(ctx, c, src, opt)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(ctx, st, src, opt, plan)
	if err != nil {
		t.Fatal(err)
	}
	// nœud ssh + web, 3 consoles (101, 200, 301), ssh 101, rdp 200
	if res.Created != 7 || res.Deleted != 0 {
		t.Fatalf("1re synchro : %+v", res)
	}
	entries, _ := st.ListSourceEntries(ctx, src.ID)
	by := map[string]store.Entry{}
	for _, e := range entries {
		by[e.ExternalID] = e
	}
	if e := by["guest/101/ssh"]; e.Host != "192.0.2.61" || e.CredentialID == nil || *e.CredentialID != cred.ID {
		t.Fatalf("ssh CT : %+v", e)
	}
	if e := by["guest/200/rdp"]; e.Kind != "rdp" || e.Host != "192.0.2.40" {
		t.Fatalf("rdp VM Windows : %+v", e)
	}
	if e := by["node/pve1/web"]; e.URL != "https://192.0.2.10:8006" {
		t.Fatalf("web nœud : %+v", e)
	}
	var s map[string]any
	json.Unmarshal(by["guest/101/console"].Settings, &s)
	if s["node"] != "pve1" || s["type"] != "lxc" || s["vmid"].(float64) != 101 {
		t.Fatalf("console : %v", s)
	}

	// L'utilisateur modifie une entrée : favori + port + notes conservés.
	e := by["guest/101/ssh"]
	e.Favorite, e.Port, e.Notes = true, 2222, "perso"
	st.SaveEntry(ctx, e)

	// 2e synchro : le CT 101 est arrêté (IP inconnue), la VM 200 a disparu.
	src, _ = st.GetSource(ctx, src.ID)
	srv2 := fakePVE(t, `,{"type":"lxc","node":"pve1","vmid":101,"name":"ct-ansible-2","status":"stopped"}`)
	defer srv2.Close()
	plan, err = CollectProxmox(ctx, proxmox.New(srv2.URL, "relay@pve!sync", "secret", false), src, opt)
	if err != nil {
		t.Fatal(err)
	}
	res, err = Apply(ctx, st, src, opt, plan)
	if err != nil {
		t.Fatal(err)
	}
	// supprimées : console 200, rdp 200, console 301 ; mise à jour : nom de la console 101
	if res.Deleted != 3 || res.Updated != 1 {
		t.Fatalf("2e synchro : %+v", res)
	}
	kept, err := st.GetEntry(ctx, e.ID)
	if err != nil || !kept.Favorite || kept.Port != 2222 || kept.Notes != "perso" {
		t.Fatalf("entrée de l'invité arrêté perdue ou écrasée : %+v %v", kept, err)
	}

	// Mauvais jeton : erreur explicite.
	if _, err := CollectProxmox(ctx, proxmox.New(srv2.URL, "relay@pve!sync", "faux", false), src, opt); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("erreur attendue : %v", err)
	}
}

func TestNetBoxSync(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer nbt_abc" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case r.URL.Path == "/api/dcim/devices/" && r.URL.Query().Get("offset") == "":
			w.Write([]byte(`{"next":"http://interne/api/dcim/devices/?limit=500&offset=500&has_primary_ip=true","results":[{"id":1,"name":"sw-coeur","primary_ip4":{"address":"192.0.2.2/24"},"site":{"name":"Example Site"},"platform":{"name":"Omada","slug":"omada"},"status":{"value":"active"}}]}`))
		case r.URL.Path == "/api/dcim/devices/":
			w.Write([]byte(`{"next":null,"results":[{"id":2,"name":"vieux","primary_ip4":{"address":"192.0.2.3/24"},"status":{"value":"decommissioning"}}]}`))
		case r.URL.Path == "/api/virtualization/virtual-machines/":
			w.Write([]byte(`{"next":null,"results":[{"id":7,"name":"srv-lab-ad1","primary_ip":{"address":"192.0.2.20/24"},"cluster":{"name":"cluster-lab"},"platform":{"name":"Windows Server 2022","slug":"windows-server-2022"},"status":{"value":"active"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	src, err := st.SaveSource(ctx, store.Source{ID: store.NewID(), Kind: store.SourceNetBox, Name: "NetBox", URL: srvURL})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := CollectNetBox(ctx, netbox.New(srvURL, "nbt_abc", true), Options{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(ctx, st, src, Options{}, plan)
	if err != nil || res.Created != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	entries, _ := st.ListSourceEntries(ctx, src.ID)
	for _, e := range entries {
		if e.Name == "srv-lab-ad1" && e.Kind != "rdp" {
			t.Fatalf("VM Windows non RDP : %+v", e)
		}
	}
	folders, _ := st.ListFolders(ctx)
	names := []string{}
	for _, f := range folders {
		names = append(names, f.Name)
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"NetBox", "Équipements", "Example Site", "Machines virtuelles", "cluster-lab"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dossier %q absent : %s", want, got)
		}
	}
	// Ré-exécution : rien ne change.
	src, _ = st.GetSource(ctx, src.ID)
	res, _ = Apply(ctx, st, src, Options{}, plan)
	if res.Created != 0 || res.Updated != 0 || res.Deleted != 0 {
		t.Fatalf("synchro non idempotente : %+v", res)
	}
}

func TestApplySkipsExcluded(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	src, _ := st.SaveSource(ctx, store.Source{ID: store.NewID(), Kind: store.SourceNetBox, Name: "NB", URL: "https://nb"})
	plan := Plan{Items: []Item{
		{ExternalID: "vm/1", Name: "a", Kind: store.KindSSH, Host: "10.0.0.1", Port: 22},
		{ExternalID: "vm/2", Name: "b", Kind: store.KindSSH, Host: "10.0.0.2", Port: 22},
	}}
	if err := st.ExcludeFromSync(ctx, src.ID, "vm/2"); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(ctx, st, src, Options{}, plan)
	if err != nil || res.Created != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if n, _ := st.ClearSyncExclusions(ctx, src.ID); n != 1 {
		t.Fatalf("exclusions effacées : %d", n)
	}
	src, _ = st.GetSource(ctx, src.ID)
	res, _ = Apply(ctx, st, src, Options{}, plan)
	if res.Created != 1 {
		t.Fatalf("entrée réautorisée non recréée : %+v", res)
	}
}
