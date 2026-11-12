# Relay Desktop – plan de travail

Décisions : client natif Rust + Slint (pas de webview) ; deux modes (serveur
Relay / base locale) ; signature gratuite SignPath Foundation plus tard ; VM
de test Windows Server 2025 (192.168.1.95, SSH `overnite\administrator`).

- [x] Étape 1 : interface native (ruban, arbre, fiche, dialogues), sources serveur + locale, coffre local chiffré, CRUD entrées/dossiers/identifiants, héritage, navigation clavier, sessions via mstsc/OpenSSH/navigateur, MSI WiX
- [ ] Étape 2 : SSH natif dans un onglet (russh + terminal), onglets de session, témoin de disponibilité
- [ ] Étape 3 : RDP intégré (ActiveX MsRdpClient dans l'onglet, supprime l'avertissement .rdp)
- [ ] Étape 4 : VNC, SFTP, commandes SSH en masse, import RDM, identifiants par dossier dans l'UI
- [ ] Signature SignPath (dépôt public requis)

Outils locaux : `~/opt/bin/rust-win cargo zigbuild --release -p relay-desktop --target x86_64-pc-windows-gnu`
puis `~/opt/bin/winvm put|run|shot|uia|ps` (tests sur la VM, session basculée sur la console via tscon).
