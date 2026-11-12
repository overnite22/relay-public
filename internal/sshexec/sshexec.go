// Package sshexec exécute une commande sur plusieurs serveurs SSH en
// parallèle et collecte, pour chacun, la sortie et le code de retour.
//
// Les clés d'hôte sont vérifiées selon le principe « confiance à la première
// connexion » : la clé vue la première fois est mémorisée (HostKeys) et toute
// clé différente ensuite fait échouer la connexion.
package sshexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Target est un serveur cible avec ses identifiants déchiffrés.
type Target struct {
	EntryID    string
	Name       string
	Host       string
	Port       int
	User       string
	Password   string
	PrivateKey string
	Passphrase string
}

// HostKeys mémorise les clés d'hôte connues (clé : "hôte:port").
type HostKeys interface {
	Get(ctx context.Context, hostPort string) (key []byte, found bool, err error)
	Put(ctx context.Context, hostPort string, key ssh.PublicKey) error
}

// Options d'exécution.
type Options struct {
	Command     string
	Sudo        bool          // exécuter avec sudo (mot de passe de l'identifiant sur stdin)
	Timeout     time.Duration // par serveur, connexion comprise
	Concurrency int
	MaxOutput   int // octets conservés par serveur
}

// États d'un résultat.
const (
	StatusPending  = "pending"
	StatusRunning  = "running"
	StatusOK       = "ok"       // code de retour 0
	StatusFailed   = "failed"   // code de retour non nul
	StatusError    = "error"    // connexion, authentification, délai...
	StatusCanceled = "canceled" // arrêt demandé
)

// Result est le résultat pour un serveur.
type Result struct {
	EntryID     string `json:"entryId"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Status      string `json:"status"`
	ExitCode    *int   `json:"exitCode"`
	Output      string `json:"output"`
	Truncated   bool   `json:"truncated"`
	Error       string `json:"error"`
	DurationMs  int64  `json:"durationMs"`
	Fingerprint string `json:"fingerprint"`
	NewHostKey  bool   `json:"newHostKey"`
}

// ErrHostKeyChanged signale une clé d'hôte différente de celle mémorisée.
var ErrHostKeyChanged = errors.New("clé d'hôte modifiée")

// HostKeyAlgorithms keeps command execution and interactive guacd sessions on
// the same server key when a host offers several. This follows libssh2's order
// in the supported guacd image, excluding legacy SHA-1 signatures.
func HostKeyAlgorithms() []string {
	return []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384,
		ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
}

// limitedBuffer conserve au plus max octets, en sécurité entre goroutines.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
			b.truncated = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.ToValidUTF8(b.buf.String(), "�"), b.truncated
}

// shellQuote entoure s de guillemets simples pour sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Run exécute la commande sur toutes les cibles. update est appelé à chaque
// changement d'état d'une cible (index dans targets).
func Run(ctx context.Context, targets []Target, opt Options, hk HostKeys, update func(i int, r Result)) {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 10
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 60 * time.Second
	}
	if opt.MaxOutput <= 0 {
		opt.MaxOutput = 512 << 10
	}
	sem := make(chan struct{}, opt.Concurrency)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				update(i, Result{EntryID: t.EntryID, Name: t.Name, Address: addr(t), Status: StatusCanceled, Error: "arrêté avant le démarrage"})
				return
			}
			defer func() { <-sem }()
			update(i, runOne(ctx, t, opt, hk, func(r Result) { update(i, r) }))
		}(i, t)
	}
	wg.Wait()
}

func addr(t Target) string {
	port := t.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(port))
}

func runOne(ctx context.Context, t Target, opt Options, hk HostKeys, progress func(Result)) Result {
	start := time.Now()
	res := Result{EntryID: t.EntryID, Name: t.Name, Address: addr(t), Status: StatusRunning}
	progress(res)
	finish := func(status, msg string) Result {
		res.Status, res.Error = status, msg
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if t.User == "" {
		return finish(StatusError, "aucun nom d'utilisateur (entrée ou identifiant)")
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	var auths []ssh.AuthMethod
	if t.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if t.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(t.PrivateKey), []byte(t.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(t.PrivateKey))
		}
		if err != nil {
			return finish(StatusError, "clé privée illisible : "+err.Error())
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if t.Password != "" {
		pw := t.Password
		auths = append(auths, ssh.Password(pw), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			out := make([]string, len(qs))
			for i := range out {
				out[i] = pw
			}
			return out, nil
		}))
	}
	if len(auths) == 0 {
		return finish(StatusError, "aucun mot de passe ni clé privée dans l'identifiant")
	}

	hostPort := addr(t)
	cfg := &ssh.ClientConfig{
		User:              t.User,
		Auth:              auths,
		HostKeyAlgorithms: HostKeyAlgorithms(),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			res.Fingerprint = ssh.FingerprintSHA256(key)
			known, found, err := hk.Get(ctx, hostPort)
			if err != nil {
				return err
			}
			if !found {
				res.NewHostKey = true
				return hk.Put(ctx, hostPort, key)
			}
			if !bytes.Equal(known, key.Marshal()) {
				if pk, err := ssh.ParsePublicKey(known); err == nil {
					return fmt.Errorf("%w : mémorisée %s, présentée %s", ErrHostKeyChanged, ssh.FingerprintSHA256(pk), res.Fingerprint)
				}
				return ErrHostKeyChanged
			}
			return nil
		},
		Timeout: 10 * time.Second,
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		if ctx.Err() != nil {
			return finish(statusFor(ctx), "délai dépassé ou arrêt demandé")
		}
		return finish(StatusError, "connexion impossible : "+cleanErr(err))
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	c, chans, reqs, err := ssh.NewClientConn(conn, hostPort, cfg)
	if err != nil {
		conn.Close()
		if ctx.Err() != nil {
			return finish(statusFor(ctx), "délai dépassé pendant la connexion")
		}
		return finish(StatusError, cleanErr(err))
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return finish(StatusError, "ouverture de session : "+cleanErr(err))
	}
	defer sess.Close()

	out := &limitedBuffer{max: opt.MaxOutput}
	sess.Stdout, sess.Stderr = out, out
	cmd := opt.Command
	if opt.Sudo {
		if t.Password == "" {
			cmd = "sudo -n -- sh -c " + shellQuote(opt.Command)
		} else {
			cmd = "sudo -S -p '' -- sh -c " + shellQuote(opt.Command)
			sess.Stdin = strings.NewReader(t.Password + "\n")
		}
	}
	runErr := sess.Run(cmd)
	res.Output, res.Truncated = out.snapshot()
	var exit *ssh.ExitError
	switch {
	case runErr == nil:
		code := 0
		res.ExitCode = &code
		return finish(StatusOK, "")
	case errors.As(runErr, &exit):
		code := exit.ExitStatus()
		res.ExitCode = &code
		return finish(StatusFailed, "")
	case ctx.Err() != nil:
		return finish(statusFor(ctx), "délai dépassé ou arrêt demandé")
	default:
		return finish(StatusError, cleanErr(runErr))
	}
}

func statusFor(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return StatusCanceled
	}
	return StatusError
}

func cleanErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "unable to authenticate"):
		return "authentification refusée (utilisateur, mot de passe ou clé)"
	case strings.Contains(s, "connection refused"):
		return "connexion refusée (port SSH fermé ?)"
	case strings.Contains(s, "i/o timeout"), strings.Contains(s, "deadline exceeded"):
		return "délai dépassé"
	case strings.Contains(s, "no route to host"):
		return "hôte injoignable"
	}
	return s
}
