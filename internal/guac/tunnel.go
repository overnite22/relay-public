package guac

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ProtocolVersion est la version du protocole annoncée à guacd.
const ProtocolVersion = "VERSION_1_5_0"

// maxInstruction borne la taille d'une instruction lue (protection mémoire).
const maxInstruction = 8 << 20

// Config décrit la connexion à ouvrir par guacd.
type Config struct {
	Protocol   string            // rdp, ssh, vnc
	Params     map[string]string // paramètres de connexion guacd (hostname, port, ...)
	Width      int
	Height     int
	DPI        int
	Timezone   string
	ImageTypes []string
	AudioTypes []string
}

// Handshake ouvre la connexion côté guacd : select → args → connect → ready.
// Il renvoie l'identifiant de connexion attribué par guacd.
func Handshake(conn net.Conn, r *bufio.Reader, cfg Config) (string, error) {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	defer conn.SetDeadline(time.Time{})

	if err := write(conn, New("select", cfg.Protocol)); err != nil {
		return "", err
	}
	args, err := expect(r, "args")
	if err != nil {
		return "", err
	}
	w, h, dpi := cfg.Width, cfg.Height, cfg.DPI
	if w <= 0 {
		w = 1280
	}
	if h <= 0 {
		h = 720
	}
	if dpi <= 0 {
		dpi = 96
	}
	images := cfg.ImageTypes
	if len(images) == 0 {
		images = []string{"image/png", "image/jpeg", "image/webp"}
	}
	steps := []Instruction{
		New("size", strconv.Itoa(w), strconv.Itoa(h), strconv.Itoa(dpi)),
		New("audio", cfg.AudioTypes...),
		New("video"),
		New("image", images...),
	}
	if cfg.Timezone != "" {
		steps = append(steps, New("timezone", cfg.Timezone))
	}
	values := make([]string, len(args.Args))
	for i, name := range args.Args {
		if strings.HasPrefix(name, "VERSION_") {
			values[i] = ProtocolVersion
			continue
		}
		values[i] = cfg.Params[name]
	}
	steps = append(steps, New("connect", values...))
	for _, in := range steps {
		if err := write(conn, in); err != nil {
			return "", err
		}
	}
	ready, err := expect(r, "ready")
	if err != nil {
		return "", err
	}
	if len(ready.Args) == 0 {
		return "", errors.New("guacd: instruction ready sans identifiant")
	}
	return ready.Args[0], nil
}

func write(w io.Writer, in Instruction) error {
	_, err := io.WriteString(w, in.Encode())
	return err
}

// readInstruction lit une instruction complète depuis r.
func readInstruction(r *bufio.Reader) (Instruction, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice(';')
		buf = append(buf, chunk...)
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				if len(buf) > maxInstruction {
					return Instruction{}, ErrMalformed
				}
				continue
			}
			return Instruction{}, err
		}
		n, elems, perr := Split(buf)
		if perr != nil {
			return Instruction{}, perr
		}
		if n == len(buf) {
			return Instruction{Opcode: elems[0], Args: elems[1:]}, nil
		}
		// Le ';' lu faisait partie d'une valeur : continuer.
		if len(buf) > maxInstruction {
			return Instruction{}, ErrMalformed
		}
	}
}

func expect(r *bufio.Reader, opcode string) (Instruction, error) {
	in, err := readInstruction(r)
	if err != nil {
		return in, fmt.Errorf("guacd: lecture de %q : %w", opcode, err)
	}
	if in.Opcode == "error" {
		msg := "erreur guacd"
		if len(in.Args) > 0 {
			msg = in.Args[0]
		}
		return in, fmt.Errorf("guacd : %s", msg)
	}
	if in.Opcode != opcode {
		return in, fmt.Errorf("guacd: %q attendu, %q reçu", opcode, in.Opcode)
	}
	return in, nil
}

// Relay copie les instructions entre le WebSocket du navigateur et guacd
// jusqu'à la fermeture d'un des deux côtés.
//
// Les instructions internes du tunnel (opcode vide, ex. "0.,4.ping,...;")
// émises par guacamole-common-js ne sont pas transmises à guacd : les ping
// sont renvoyés au navigateur comme le fait le tunnel Java officiel.
func Relay(ctx context.Context, ws *websocket.Conn, conn net.Conn, r *bufio.Reader, tunnelID string, recording *Recording) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wmu sync.Mutex
	send := func(s string) error {
		wmu.Lock()
		defer wmu.Unlock()
		return ws.Write(ctx, websocket.MessageText, []byte(s))
	}
	if err := send(New("", tunnelID).Encode()); err != nil {
		return err
	}

	errc := make(chan error, 2)

	// guacd → navigateur : n'envoyer que des instructions complètes.
	go func() {
		buf := make([]byte, 0, 64<<10)
		tmp := make([]byte, 32<<10)
		for {
			k, err := r.Read(tmp)
			if k > 0 {
				buf = append(buf, tmp[:k]...)
				end := 0
				for {
					n, elems, perr := Split(buf[end:])
					if perr != nil {
						errc <- perr
						return
					}
					if n == 0 {
						break
					}
					if recording != nil {
						recording.Capture(buf[end:end+n], elems)
					}
					end += n
				}
				if end > 0 {
					if werr := send(string(buf[:end])); werr != nil {
						errc <- werr
						return
					}
					buf = append(buf[:0], buf[end:]...)
				}
				if len(buf) > maxInstruction {
					errc <- ErrMalformed
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	// navigateur → guacd.
	go func() {
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			off := 0
			for off < len(data) {
				n, elems, perr := Split(data[off:])
				if perr != nil || n == 0 {
					errc <- ErrMalformed
					return
				}
				raw := data[off : off+n]
				off += n
				if elems[0] == "" {
					if len(elems) > 1 && elems[1] == "ping" {
						if werr := send(string(raw)); werr != nil {
							errc <- werr
							return
						}
					}
					continue
				}
				if _, werr := conn.Write(raw); werr != nil {
					errc <- werr
					return
				}
			}
		}
	}()

	err := <-errc
	cancel()
	_ = conn.Close()
	<-errc // Les deux boucles ont terminé avant de finaliser la capture.
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) ||
		websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return nil
	}
	return err
}
