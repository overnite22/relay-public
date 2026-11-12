package rdmimport

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overnite22/relay-public/internal/store"
)

// Échantillon représentatif d'un export RDM (structure ArrayOfConnection).
const sample = `<?xml version="1.0"?>
<ArrayOfConnection>
  <Connection>
    <ConnectionType>Group</ConnectionType>
    <Group>LINUX LXC</Group>
    <ID>11111111-0000-0000-0000-000000000001</ID>
    <Name>LINUX LXC</Name>
  </Connection>
  <Connection>
    <ConnectionType>Credential</ConnectionType>
    <ID>CCCCCCCC-0000-0000-0000-000000000001</ID>
    <Name>Lab Administrator</Name>
    <Credentials>
      <UserName>Administrator</UserName>
      <Domain>EXAMPLE</Domain>
      <SafePassword>QkFTRTY0RU5DUllQVEVE</SafePassword>
    </Credentials>
  </Connection>
  <Connection>
    <ConnectionType>SSHShell</ConnectionType>
    <Group>LINUX LXC</Group>
    <ID>22222222-0000-0000-0000-000000000001</ID>
    <Name>srv-lab-ans</Name>
    <Terminal>
      <Host>192.0.2.61</Host>
      <HostPort>2222</HostPort>
      <Username>root</Username>
      <SafePassword>xxxx</SafePassword>
    </Terminal>
    <MetaInformation><Host>ne-pas-prendre</Host></MetaInformation>
  </Connection>
  <Connection>
    <ConnectionType>RDPConfigured</ConnectionType>
    <Group>WINDOWS</Group>
    <ID>33333333-0000-0000-0000-000000000001</ID>
    <Name>srv-lab-ad1</Name>
    <Url>192.0.2.20:3390</Url>
    <CredentialConnectionID>cccccccc-0000-0000-0000-000000000001</CredentialConnectionID>
    <Description>Contrôleur de domaine</Description>
  </Connection>
  <Connection>
    <ConnectionType>RDPConfigured</ConnectionType>
    <Group>WINDOWS\Sous-dossier</Group>
    <Name>srv-lab-rapps</Name>
    <Url>srv-lab-rapps.lab</Url>
    <CredentialConnectionID>99999999-0000-0000-0000-000000000000</CredentialConnectionID>
  </Connection>
  <Connection>
    <ConnectionType>WebBrowser</ConnectionType>
    <Group>RESEAU</Group>
    <Name>Omada</Name>
    <WebBrowserUrl>192.0.2.249:8043</WebBrowserUrl>
  </Connection>
  <Connection>
    <ConnectionType>TeamViewer</ConnectionType>
    <Name>Unsupported session</Name>
  </Connection>
  <Connection>
    <ConnectionType>SSHShell</ConnectionType>
    <Name>Sans hôte</Name>
  </Connection>
</ArrayOfConnection>`

func TestParse(t *testing.T) {
	p, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Entries) != 4 {
		t.Fatalf("entrées : %+v", p.Entries)
	}
	by := map[string]Entry{}
	for _, e := range p.Entries {
		by[e.Name] = e
	}
	if e := by["srv-lab-ans"]; e.Kind != "ssh" || e.Host != "192.0.2.61" || e.Port != 2222 || e.Username != "root" || !e.InlineSecret {
		t.Errorf("ssh : %+v", e)
	}
	if e := by["srv-lab-ad1"]; e.Kind != "rdp" || e.Host != "192.0.2.20" || e.Port != 3390 || e.Credential != "Lab Administrator" || e.Notes != "Contrôleur de domaine" {
		t.Errorf("rdp : %+v", e)
	}
	if e := by["srv-lab-rapps"]; e.Port != 3389 || strings.Join(e.Path, "/") != "WINDOWS/Sous-dossier" || e.CredentialRef != "" {
		t.Errorf("rdp 2 : %+v", e)
	}
	if e := by["Omada"]; e.Kind != "web" || e.URL != "https://192.0.2.249:8043" {
		t.Errorf("web : %+v", e)
	}
	if len(p.Credentials) != 1 || p.Credentials[0].Username != "Administrator" || p.Credentials[0].Domain != "EXAMPLE" || !p.Credentials[0].EncryptedSecret {
		t.Errorf("identifiants : %+v", p.Credentials)
	}
	if len(p.Skipped) != 2 {
		t.Errorf("ignorées : %+v", p.Skipped)
	}
	if len(p.Warnings) != 3 {
		t.Errorf("avertissements : %q", p.Warnings)
	}
	paths := []string{}
	for _, f := range p.Folders {
		paths = append(paths, strings.Join(f.Path, "/"))
	}
	if strings.Join(paths, ",") != "LINUX LXC,WINDOWS,WINDOWS/Sous-dossier,RESEAU" {
		t.Errorf("dossiers : %v", paths)
	}
}

func TestParseRejectsOtherXML(t *testing.T) {
	if _, err := Parse(strings.NewReader(`<html><body/></html>`)); err == nil {
		t.Fatal("XML quelconque accepté")
	}
	if _, err := Parse(strings.NewReader(`pas du xml`)); err == nil {
		t.Fatal("texte accepté")
	}
}

func TestApply(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, _ := Parse(strings.NewReader(sample))
	res, err := Apply(ctx, st, p, "Import RDM", Defaults{SSH: "Lab Administrator"}, func(id, field, v string) (string, error) {
		if v == "" {
			return "", nil
		}
		return "sealed:" + v, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Entries != 4 || res.Credentials != 1 || res.Folders != 4 {
		t.Fatalf("%+v", res)
	}
	entries, _ := st.ListEntries(ctx)
	creds, _ := st.ListCredentials(ctx)
	for _, e := range entries {
		if e.Name == "srv-lab-ad1" && (e.CredentialID == nil || *e.CredentialID != creds[0].ID) {
			t.Fatalf("identifiant non lié : %+v", e)
		}
	}
}

// Cas observés dans un export réel : enveloppe RDMExport, fiche Hôte,
// identifiants spéciaux (hériter / aucun), clé privée chiffrée, entrée à la racine.
func TestParseRealWorldShapes(t *testing.T) {
	const x = `<?xml version="1.0" encoding="utf-8"?>
<RDMExport><Connections>
  <Connection><ConnectionType>Host</ConnectionType><Group>ARCHIVES</Group><ID>11111111-2222-4333-8444-555555555555</ID><Name>OCI</Name>
    <HostDetails><Host>203.0.113.29</Host><UserName>ubuntu</UserName></HostDetails></Connection>
  <Connection><ConnectionType>SSHShell</ConnectionType><Group>ARCHIVES</Group><HostConnectionID>11111111-2222-4333-8444-555555555555</HostConnectionID><Name>SSH OCI</Name>
    <Terminal><SafePrivateKeyData>xxx</SafePrivateKeyData></Terminal></Connection>
  <Connection><ConnectionType>SSHShell</ConnectionType><CredentialConnectionID>245A4245-48E7-4DF5-9C4C-11861D8E1F81</CredentialConnectionID><Group>LINUX LXC</Group><Name>srv-lab-mc</Name>
    <Terminal><Host>192.0.2.58</Host></Terminal></Connection>
  <Connection><ConnectionType>Group</ConnectionType><CredentialConnectionID>B87B29D9-9239-4D7B-86D8-9B53DCD3BA9F</CredentialConnectionID><Group>WINDOWS</Group><Name>WINDOWS</Name></Connection>
  <Connection><ConnectionType>Credential</ConnectionType><ID>22222222-3333-4444-8555-666666666666</ID><Name>Lab SSH Key</Name>
    <Credentials><CredentialType>PrivateKey</CredentialType><PrivateKeyOverrideUsername>root</PrivateKeyOverrideUsername><PrivateKeyType>Data</PrivateKeyType><SafePrivateKeyData>yyy</SafePrivateKeyData></Credentials></Connection>
  <Connection><ConnectionType>RDPConfigured</ConnectionType><Name>VBR</Name><Url>SRV-LAB-VBR.EXAMPLE.COM</Url></Connection>
  <Connection><ConnectionType>ProxmoxConsole</ConnectionType><Name>[DASHPROX]</Name></Connection>
</Connections></RDMExport>`
	p, err := Parse(strings.NewReader(x))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Entry{}
	for _, e := range p.Entries {
		by[e.Name] = e
	}
	if e := by["SSH OCI"]; e.Host != "203.0.113.29" || e.Username != "ubuntu" || !e.InlineSecret {
		t.Errorf("session de la fiche hôte : %+v", e)
	}
	if e := by["VBR"]; e.Path == nil {
		t.Errorf("chemin nil pour une entrée à la racine (JSON null) : %+v", e)
	}
	if c := p.Credentials[0]; c.Username != "root" || !c.EncryptedSecret {
		t.Errorf("clé privée : %+v", c)
	}
	for _, w := range p.Warnings {
		if strings.Contains(w, "absent de l'export") {
			t.Errorf("identifiant spécial signalé comme absent : %q", w)
		}
	}
	if len(p.Skipped) != 2 {
		t.Errorf("ignorées : %+v", p.Skipped)
	}
}

func TestRejectsExcessiveLogicalFolderDepth(t *testing.T) {
	xml := "<ArrayOfConnection><Connection><Name>test</Name><ConnectionType>SSHShell</ConnectionType><Group>" + strings.Repeat("a\\", 10000) + "</Group><Host>example.test</Host></Connection></ArrayOfConnection>"
	if _, err := Parse(strings.NewReader(xml)); err == nil {
		t.Fatal("unbounded folder path accepted")
	}
}
