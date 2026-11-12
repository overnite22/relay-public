package sshexec

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type memKeys struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *memKeys) Get(_ context.Context, hp string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[hp]
	return v, ok, nil
}

func (k *memKeys) Put(_ context.Context, hp string, key ssh.PublicKey) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[hp] = key.Marshal()
	return nil
}

// testServer démarre un serveur SSH minimal qui interprète quelques commandes.
func testServer(t *testing.T) (addr string) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if c.User() == "u" && string(pw) == "pw" {
			return nil, nil
		}
		return nil, errAuth
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func(nc net.Conn) {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					ch, creqs, _ := nch.Accept()
					go func(ch ssh.Channel, creqs <-chan *ssh.Request) {
						defer ch.Close()
						for req := range creqs {
							if req.Type != "exec" {
								req.Reply(false, nil)
								continue
							}
							req.Reply(true, nil)
							cmd := string(req.Payload[4:])
							code := 0
							switch {
							case cmd == "echo hi":
								ch.Write([]byte("hi\n"))
							case cmd == "exit 3":
								ch.Stderr().Write([]byte("erreur\n"))
								code = 3
							case cmd == "sleep":
								time.Sleep(3 * time.Second)
							case strings.HasPrefix(cmd, "sudo -S -p '' -- sh -c "):
								line, _ := bufio.NewReader(ch).ReadString('\n')
								if line != "pw\n" {
									code = 1
								}
								ch.Write([]byte("root via " + strings.TrimPrefix(cmd, "sudo -S -p '' -- sh -c ") + "\n"))
							case cmd == "big":
								ch.Write([]byte(strings.Repeat("x", 5000)))
							default:
								code = 127
							}
							st := make([]byte, 4)
							binary.BigEndian.PutUint32(st, uint32(code))
							ch.SendRequest("exit-status", false, st)
							return
						}
					}(ch, creqs)
				}
			}(nc)
		}
	}()
	return ln.Addr().String()
}

var errAuth = &authErr{}

type authErr struct{}

func (*authErr) Error() string { return "refusé" }

func run(t *testing.T, targets []Target, opt Options, hk HostKeys) []Result {
	t.Helper()
	out := make([]Result, len(targets))
	var mu sync.Mutex
	Run(context.Background(), targets, opt, hk, func(i int, r Result) {
		mu.Lock()
		out[i] = r
		mu.Unlock()
	})
	return out
}

func TestRunManyHosts(t *testing.T) {
	addr := testServer(t)
	host, portS, _ := net.SplitHostPort(addr)
	var port int
	for _, c := range portS {
		port = port*10 + int(c-'0')
	}
	hk := &memKeys{m: map[string][]byte{}}
	ok := Target{Name: "a", Host: host, Port: port, User: "u", Password: "pw"}
	bad := ok
	bad.Name, bad.Password = "mauvais mdp", "nope"
	nouser := ok
	nouser.Name, nouser.User = "sans utilisateur", ""
	closed := Target{Name: "fermé", Host: "127.0.0.1", Port: 1, User: "u", Password: "pw"}

	first := run(t, []Target{ok}, Options{Command: "echo hi"}, hk)
	if first[0].Status != StatusOK || !first[0].NewHostKey {
		t.Fatalf("première connexion : %+v", first[0])
	}
	res := run(t, []Target{ok, bad, nouser, closed}, Options{Command: "echo hi", Timeout: 5 * time.Second}, hk)
	if res[0].Status != StatusOK || res[0].Output != "hi\n" || *res[0].ExitCode != 0 || res[0].NewHostKey || !strings.HasPrefix(res[0].Fingerprint, "SHA256:") {
		t.Errorf("ok : %+v", res[0])
	}
	if res[1].Status != StatusError || !strings.Contains(res[1].Error, "authentification") {
		t.Errorf("mauvais mdp : %+v", res[1])
	}
	if res[2].Status != StatusError || !strings.Contains(res[2].Error, "utilisateur") {
		t.Errorf("sans utilisateur : %+v", res[2])
	}
	if res[3].Status != StatusError || !strings.Contains(res[3].Error, "refusée") {
		t.Errorf("port fermé : %+v", res[3])
	}

	// Code de retour non nul et stderr capturé.
	res = run(t, []Target{ok}, Options{Command: "exit 3"}, hk)
	if res[0].Status != StatusFailed || *res[0].ExitCode != 3 || res[0].Output != "erreur\n" || res[0].NewHostKey {
		t.Errorf("exit 3 : %+v", res[0])
	}
	// sudo : mot de passe envoyé sur stdin, commande protégée par des guillemets.
	res = run(t, []Target{ok}, Options{Command: "id -u; echo 'x'", Sudo: true}, hk)
	if res[0].Status != StatusOK || res[0].Output != `root via 'id -u; echo '\''x'\'''`+"\n" {
		t.Errorf("sudo : %+v %q", res[0], res[0].Output)
	}
	// Sortie tronquée.
	res = run(t, []Target{ok}, Options{Command: "big", MaxOutput: 1000}, hk)
	if len(res[0].Output) != 1000 || !res[0].Truncated {
		t.Errorf("troncature : %d %v", len(res[0].Output), res[0].Truncated)
	}
	// Délai dépassé.
	res = run(t, []Target{ok}, Options{Command: "sleep", Timeout: 500 * time.Millisecond}, hk)
	if res[0].Status != StatusError || !strings.Contains(res[0].Error, "délai") {
		t.Errorf("délai : %+v", res[0])
	}
	// Clé d'hôte modifiée : refus.
	for k := range hk.m {
		_, other, _ := ed25519.GenerateKey(rand.Reader)
		s, _ := ssh.NewSignerFromKey(other)
		hk.m[k] = s.PublicKey().Marshal()
	}
	res = run(t, []Target{ok}, Options{Command: "echo hi"}, hk)
	if res[0].Status != StatusError || !strings.Contains(res[0].Error, "clé d'hôte modifiée") {
		t.Errorf("clé modifiée : %+v", res[0])
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Fatal(got)
	}
}

func TestCanceledQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var results []Result
	var mu sync.Mutex
	Run(ctx, []Target{{Name: "a", User: "u", Host: "127.0.0.1", Port: 1, Password: "pw"}, {Name: "b", User: "u", Host: "127.0.0.1", Port: 1, Password: "pw"}}, Options{Command: "sleep", Concurrency: 1}, &memKeys{m: map[string][]byte{}}, func(_ int, r Result) {
		if r.Status != StatusRunning {
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}
	})
	if len(results) != 2 {
		t.Fatalf("results: %+v", results)
	}
	for _, r := range results {
		if r.Status != StatusCanceled {
			t.Fatalf("cancel: %+v", r)
		}
	}
}
