package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/overnite22/relay-public/internal/sshexec"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSH key exchange verifies possession of the host key before this callback.
// Stop before user authentication, without sending any password or private key.
func (s *Server) verifySSHHostKey(ctx context.Context, address string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	checked := errors.New("host key verified")
	var key ssh.PublicKey
	config := &ssh.ClientConfig{User: "relay-hostkey-check", HostKeyAlgorithms: sshexec.HostKeyAlgorithms(), HostKeyCallback: func(_ string, _ net.Addr, presented ssh.PublicKey) error {
		key = presented
		return checked
	}}
	_, _, _, err = ssh.NewClientConn(conn, address, config)
	if !errors.Is(err, checked) || key == nil {
		return "", fmt.Errorf("vérification de la clé SSH : %w", err)
	}
	known, found, err := s.opt.Store.KnownHostKey(ctx, address)
	if err != nil {
		return "", err
	}
	if found && string(known) != string(key.Marshal()) {
		return "", fmt.Errorf("clé d’hôte SSH modifiée pour %s : vérifiez le serveur avant d’oublier l’ancienne clé", address)
	}
	if err := s.opt.Store.RememberHostKey(ctx, address, key.Marshal(), ssh.FingerprintSHA256(key)); err != nil {
		return "", err
	}
	return knownhosts.Normalize(address) + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), nil
}
