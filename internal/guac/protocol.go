// Package guac implémente la partie du protocole Guacamole nécessaire pour
// relayer une session entre le navigateur (guacamole-common-js) et guacd.
//
// Une instruction est une suite d'éléments "LONGUEUR.VALEUR" séparés par des
// virgules et terminée par un point-virgule. LONGUEUR compte des caractères
// Unicode (points de code), pas des octets.
package guac

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrMalformed signale une instruction mal formée.
var ErrMalformed = errors.New("guac: instruction mal formée")

// Instruction est une instruction décodée.
type Instruction struct {
	Opcode string
	Args   []string
}

// Encode sérialise l'instruction.
func (in Instruction) Encode() string {
	var b strings.Builder
	writeElem(&b, in.Opcode)
	for _, a := range in.Args {
		b.WriteByte(',')
		writeElem(&b, a)
	}
	b.WriteByte(';')
	return b.String()
}

// New construit une instruction.
func New(opcode string, args ...string) Instruction { return Instruction{Opcode: opcode, Args: args} }

func writeElem(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(utf8.RuneCountInString(s)))
	b.WriteByte('.')
	b.WriteString(s)
}

// Split cherche la fin de la première instruction complète de buf.
// Il renvoie la longueur en octets de l'instruction (0 si incomplète) et les
// éléments décodés. Une erreur signale un flux invalide.
func Split(buf []byte) (n int, elems []string, err error) {
	i := 0
	for {
		// Longueur décimale.
		start := i
		for i < len(buf) && buf[i] >= '0' && buf[i] <= '9' {
			i++
		}
		if i == len(buf) {
			return 0, nil, nil
		}
		if i == start || buf[i] != '.' || i-start > 9 {
			return 0, nil, ErrMalformed
		}
		length, _ := strconv.Atoi(string(buf[start:i]))
		i++ // '.'
		valStart := i
		for r := 0; r < length; r++ {
			if i >= len(buf) {
				return 0, nil, nil
			}
			if !utf8.FullRune(buf[i:]) {
				return 0, nil, nil
			}
			c, size := utf8.DecodeRune(buf[i:])
			if c == utf8.RuneError && size == 1 {
				return 0, nil, ErrMalformed
			}
			i += size
		}
		if i >= len(buf) {
			return 0, nil, nil
		}
		elems = append(elems, string(buf[valStart:i]))
		switch buf[i] {
		case ',':
			i++
		case ';':
			return i + 1, elems, nil
		default:
			return 0, nil, ErrMalformed
		}
	}
}

// Parse décode exactement une instruction complète.
func Parse(s string) (Instruction, error) {
	n, elems, err := Split([]byte(s))
	if err != nil {
		return Instruction{}, err
	}
	if n == 0 || n != len(s) {
		return Instruction{}, ErrMalformed
	}
	return Instruction{Opcode: elems[0], Args: elems[1:]}, nil
}
