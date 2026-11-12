// Package syncer crée et met à jour les entrées Relay à partir des sources
// externes (Proxmox VE, NetBox).
//
// Règles :
//   - une entrée synchronisée est identifiée par (source, identifiant externe) ;
//   - à la création, elle reçoit le dossier calculé et l'identifiant par défaut ;
//   - à la mise à jour, seuls le nom, l'hôte, le port, l'URL et les paramètres
//     propres à la source (nœud, VMID) sont rafraîchis : le dossier,
//     l'identifiant, les notes, les favoris et les réglages choisis par
//     l'utilisateur sont conservés ;
//   - une entrée qui n'existe plus côté source est supprimée, sauf si son
//     identifiant figure dans Keep (invité arrêté dont l'IP est inconnue).
package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/overnite22/relay-public/internal/netbox"
	"github.com/overnite22/relay-public/internal/proxmox"
	"github.com/overnite22/relay-public/internal/store"
)

// Item est une entrée souhaitée.
type Item struct {
	ExternalID string
	Name       string
	Kind       string
	Host       string
	Port       int
	URL        string
	Folder     []string       // chemin relatif au dossier de la source
	Managed    map[string]any // paramètres imposés par la source (fusionnés)
	Notes      string         // notes initiales (création seulement)
	CredRole   string         // "ssh", "rdp" ou "" : identifiant par défaut à appliquer
}

// Plan est le résultat de la collecte.
type Plan struct {
	Items []Item
	Keep  []string
}

// Options communes aux sources (colonne options, JSON).
type Options struct {
	SSHCredentialID string `json:"sshCredentialId"`
	RDPCredentialID string `json:"rdpCredentialId"`
	GuestEntries    *bool  `json:"guestEntries"` // Proxmox : entrées SSH/RDP des invités (défaut : oui)
	NodeEntries     *bool  `json:"nodeEntries"`  // Proxmox : entrées SSH + web des nœuds (défaut : oui)
	Devices         *bool  `json:"devices"`      // NetBox : équipements (défaut : oui)
	VMs             *bool  `json:"vms"`          // NetBox : machines virtuelles (défaut : oui)
}

func on(b *bool) bool { return b == nil || *b }

// Result résume une synchronisation.
type Result struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
	Total   int `json:"total"`
}

func (r Result) String() string {
	return fmt.Sprintf("%d entrée(s) : %d créée(s), %d mise(s) à jour, %d supprimée(s)", r.Total, r.Created, r.Updated, r.Deleted)
}

// ---------- Collecte Proxmox ----------

// CollectProxmox construit le plan d'un cluster Proxmox VE.
func CollectProxmox(ctx context.Context, c *proxmox.Client, src store.Source, opt Options) (Plan, error) {
	res, err := c.Resources(ctx)
	if err != nil {
		return Plan{}, err
	}
	ips, err := c.NodeIPs(ctx)
	if err != nil {
		ips = map[string]string{}
	}
	var p Plan
	type guest struct {
		r    proxmox.Resource
		info proxmox.GuestInfo
	}
	var guests []*guest
	for _, r := range res {
		switch r.Type {
		case "node":
			if !on(opt.NodeEntries) {
				continue
			}
			host := ips[r.Node]
			if host == "" {
				host = r.Node
			}
			p.Items = append(p.Items,
				Item{ExternalID: "node/" + r.Node + "/ssh", Name: r.Node, Kind: store.KindSSH, Host: host, Port: 22, Folder: []string{"Nœuds"}, CredRole: "ssh",
					Notes: "Nœud Proxmox VE (synchronisé depuis " + src.Name + ")."},
				Item{ExternalID: "node/" + r.Node + "/web", Name: r.Node + " (web)", Kind: store.KindWeb, URL: "https://" + host + ":8006", Folder: []string{"Nœuds"}},
			)
		case "qemu", "lxc":
			if r.Template == 1 {
				continue
			}
			guests = append(guests, &guest{r: r})
		}
	}
	// Découverte des IP en parallèle (8 requêtes max, 6 s chacune).
	if on(opt.GuestEntries) {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for _, g := range guests {
			if g.r.Status != "running" {
				continue
			}
			wg.Add(1)
			go func(g *guest) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				gctx, cancel := context.WithTimeout(ctx, 6*time.Second)
				defer cancel()
				g.info = c.Guest(gctx, g.r.Node, g.r.Type, g.r.VMID)
			}(g)
		}
		wg.Wait()
	}
	for _, g := range guests {
		r := g.r
		name := r.Name
		if name == "" {
			name = fmt.Sprintf("%s-%d", r.Type, r.VMID)
		}
		folder := "VM"
		if r.Type == "lxc" {
			folder = "LXC"
		}
		id := fmt.Sprintf("guest/%d", r.VMID)
		p.Items = append(p.Items, Item{
			ExternalID: id + "/console", Name: name + " (console)", Kind: store.KindPVE, Folder: []string{folder},
			Managed: map[string]any{"sourceId": src.ID, "node": r.Node, "vmid": r.VMID, "type": r.Type},
			Notes:   fmt.Sprintf("%s %d (synchronisé depuis %s).", strings.ToUpper(r.Type), r.VMID, src.Name),
		})
		if !on(opt.GuestEntries) {
			continue
		}
		switch {
		case g.info.IPv4 != "" && g.info.Windows:
			p.Items = append(p.Items, Item{ExternalID: id + "/rdp", Name: name, Kind: store.KindRDP, Host: g.info.IPv4, Port: 3389, Folder: []string{folder}, CredRole: "rdp"})
		case g.info.IPv4 != "":
			p.Items = append(p.Items, Item{ExternalID: id + "/ssh", Name: name, Kind: store.KindSSH, Host: g.info.IPv4, Port: 22, Folder: []string{folder}, CredRole: "ssh"})
		default:
			// Invité arrêté ou sans agent : on garde l'entrée existante telle quelle.
			p.Keep = append(p.Keep, id+"/ssh", id+"/rdp")
		}
	}
	return p, nil
}

// ---------- Collecte NetBox ----------

func isWindows(o netbox.Object) bool {
	if o.Platform == nil {
		return false
	}
	return strings.Contains(strings.ToLower(o.Platform.Slug+" "+o.Platform.Name), "windows")
}

// CollectNetBox construit le plan d'une instance NetBox.
func CollectNetBox(ctx context.Context, c *netbox.Client, opt Options) (Plan, error) {
	var p Plan
	add := func(prefix, top string, objs []netbox.Object, group func(netbox.Object) string) {
		for _, o := range objs {
			if o.Status.Value == "decommissioning" || o.Name == "" {
				continue
			}
			ip := o.Address()
			if ip == "" {
				continue
			}
			it := Item{ExternalID: fmt.Sprintf("%s/%d", prefix, o.ID), Name: o.Name, Host: ip, Folder: []string{top, group(o)}}
			if isWindows(o) {
				it.Kind, it.Port, it.CredRole = store.KindRDP, 3389, "rdp"
			} else {
				it.Kind, it.Port, it.CredRole = store.KindSSH, 22, "ssh"
			}
			var notes []string
			if o.Description != "" {
				notes = append(notes, o.Description)
			}
			if r := o.RoleName(); r != "" {
				notes = append(notes, "Rôle : "+r)
			}
			if o.Platform != nil {
				notes = append(notes, "Plateforme : "+o.Platform.Name)
			}
			if o.DisplayURL != "" {
				notes = append(notes, "NetBox : "+o.DisplayURL)
			}
			it.Notes = strings.Join(notes, "\n")
			p.Items = append(p.Items, it)
		}
	}
	if on(opt.Devices) {
		devs, err := c.Devices(ctx)
		if err != nil {
			return p, err
		}
		add("device", "Équipements", devs, func(o netbox.Object) string {
			if o.Site != nil {
				return o.Site.Name
			}
			return "Sans site"
		})
	}
	if on(opt.VMs) {
		vms, err := c.VirtualMachines(ctx)
		if err != nil {
			return p, err
		}
		add("vm", "Machines virtuelles", vms, func(o netbox.Object) string {
			if o.Cluster != nil {
				return o.Cluster.Name
			}
			if o.Site != nil {
				return o.Site.Name
			}
			return "Sans cluster"
		})
	}
	return p, nil
}

// ---------- Application ----------

// Apply applique un plan à la base pour la source src.
func Apply(ctx context.Context, st *store.Store, src store.Source, opt Options, p Plan) (Result, error) {
	var res Result
	// Dossier racine de la source.
	root := ""
	if src.FolderID != nil && st.FolderExists(ctx, *src.FolderID) {
		root = *src.FolderID
	} else {
		id, err := st.EnsureFolder(ctx, nil, src.Name)
		if err != nil {
			return res, err
		}
		root = id
		if err := st.SetSourceFolder(ctx, src.ID, id); err != nil {
			return res, err
		}
	}
	folderCache := map[string]string{"": root}
	folderFor := func(path []string) (string, error) {
		key := ""
		parent := root
		for _, part := range path {
			key += "\x00" + part
			if id, ok := folderCache[key]; ok {
				parent = id
				continue
			}
			p := parent
			id, err := st.EnsureFolder(ctx, &p, part)
			if err != nil {
				return "", err
			}
			folderCache[key] = id
			parent = id
		}
		return parent, nil
	}

	existing, err := st.ListSourceEntries(ctx, src.ID)
	if err != nil {
		return res, err
	}
	excluded, err := st.SyncExclusions(ctx, src.ID)
	if err != nil {
		return res, err
	}
	byExt := map[string]store.Entry{}
	for _, e := range existing {
		byExt[e.ExternalID] = e
	}
	keep := map[string]bool{}
	for _, k := range p.Keep {
		keep[k] = true
	}
	seen := map[string]bool{}
	// Ordre stable (journal et tests lisibles).
	sort.SliceStable(p.Items, func(i, j int) bool { return p.Items[i].ExternalID < p.Items[j].ExternalID })

	for _, it := range p.Items {
		if seen[it.ExternalID] {
			continue
		}
		seen[it.ExternalID] = true
		cur, ok := byExt[it.ExternalID]
		if !ok {
			if excluded[it.ExternalID] {
				continue // supprimée ou fusionnée par l'utilisateur
			}
			folderID, err := folderFor(it.Folder)
			if err != nil {
				return res, err
			}
			settings, _ := json.Marshal(nonNil(it.Managed))
			e := store.Entry{
				FolderID: &folderID, Name: it.Name, Kind: it.Kind, Host: it.Host, Port: it.Port, URL: it.URL,
				Settings: settings, Notes: it.Notes, SourceID: &src.ID, ExternalID: it.ExternalID,
			}
			switch it.CredRole {
			case "ssh":
				e.CredentialID = optional(opt.SSHCredentialID)
			case "rdp":
				e.CredentialID = optional(opt.RDPCredentialID)
			}
			if _, err := st.SaveEntry(ctx, e); err != nil {
				// Identifiant par défaut supprimé entre-temps : on crée sans.
				e.CredentialID = nil
				if _, err2 := st.SaveEntry(ctx, e); err2 != nil {
					return res, fmt.Errorf("%s : %w", it.Name, err2)
				}
			}
			res.Created++
			continue
		}
		upd := cur
		// Le port n'est pas rafraîchi : la source ne le connaît pas, l'utilisateur a pu le changer.
		upd.Name, upd.Host, upd.URL = it.Name, it.Host, it.URL
		if it.Kind != cur.Kind {
			upd.Kind, upd.Port = it.Kind, it.Port
		}
		if len(it.Managed) > 0 {
			var settings map[string]any
			_ = json.Unmarshal(cur.Settings, &settings)
			if settings == nil {
				settings = map[string]any{}
			}
			for k, v := range it.Managed {
				settings[k] = v
			}
			upd.Settings, _ = json.Marshal(settings)
		}
		if changed(cur, upd) {
			if _, err := st.SaveEntry(ctx, upd); err != nil {
				return res, fmt.Errorf("%s : %w", it.Name, err)
			}
			res.Updated++
		}
	}
	for ext, e := range byExt {
		if seen[ext] || keep[ext] {
			continue
		}
		if err := st.DeleteEntry(ctx, e.ID); err != nil {
			return res, err
		}
		res.Deleted++
	}
	res.Total = len(seen)
	for ext := range byExt {
		if keep[ext] && !seen[ext] {
			res.Total++
		}
	}
	return res, nil
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func optional(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func changed(a, b store.Entry) bool {
	if a.Name != b.Name || a.Host != b.Host || a.Port != b.Port || a.URL != b.URL || a.Kind != b.Kind {
		return true
	}
	var x, y any
	_ = json.Unmarshal(a.Settings, &x)
	_ = json.Unmarshal(b.Settings, &y)
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) != string(by)
}
