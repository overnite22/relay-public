# Relay Desktop – plan de travail

Décisions : Tauri 2 + interface React existante ; client connecté à un serveur
Relay d'abord, autonome en option ; signature gratuite SignPath Foundation ; VM
Windows de test plus tard.

- [x] P0 Fondations : workspace Rust, proxy loopback + appairage (tests), shell Tauri, CI MSI non signé
- [ ] P1 Client connecté : valider le MSI sur Windows, lien `relay://` (deep link + confirmation), menu Déconnexion/Changer de serveur, gestion du jeton révoqué
- [ ] P2 Sessions natives : route serveur de prêt d'identifiant (audit, désactivable), SSH russh + xterm.js + SFTP, RDP ActiveX MsRdpClient + mstsc externe, VNC via noVNC + pont local
- [ ] P3 Intégration Windows : onglets détachables, zone de notification, mise à jour signée, cache hors ligne chiffré
- [ ] P4 MSI : propriétés `SERVERURL` pour GPO, signature SignPath, SBOM, publication
- [ ] P5 (option) mode autonome

Outils locaux : `~/opt/bin/rust-env` (Rust + Zig comme cc). Vérif Windows :
`CC_x86_64_pc_windows_gnu=cc-win AR_x86_64_pc_windows_gnu=ar rust-env cargo clippy -p relay-desktop --target x86_64-pc-windows-gnu`.
