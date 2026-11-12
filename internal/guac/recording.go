package guac

import (
	"io"
)

// Recording captures display instructions and the image streams they reference.
// The caller supplies only instructions received from guacd, before forwarding.
type Recording struct {
	w       io.Writer
	limit   int64
	written int64
	frame   int64
	images  map[string]bool
	limited bool
	err     error
}

func NewRecording(w io.Writer, limit int64) *Recording {
	return &Recording{w: w, limit: limit, images: make(map[string]bool)}
}

func (r *Recording) Capture(raw []byte, elems []string) {
	if r.err != nil || r.limited {
		return
	}
	switch elems[0] {
	case "img":
		if len(elems) < 7 {
			return
		}
		r.images[elems[1]] = true
	case "blob", "end":
		if len(elems) < 2 || !r.images[elems[1]] {
			return
		}
		if elems[0] == "end" {
			delete(r.images, elems[1])
		}
	case "arc", "cfill", "clip", "close", "copy", "cstroke", "cursor",
		"curve", "dispose", "distort", "identity", "jpeg", "lfill", "line",
		"lstroke", "move", "png", "pop", "push", "rect", "reset", "set",
		"shade", "size", "start", "sync", "transfer", "transform":
		// Instructions de dessin et délimiteurs d'image.
	default:
		return
	}
	if int64(len(raw)) > r.limit-r.written {
		r.limited = true
		return
	}
	n, err := r.w.Write(raw)
	r.written += int64(n)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	r.err = err
	if err == nil && elems[0] == "sync" && len(r.images) == 0 {
		r.frame = r.written
	}
}

// Result returns the last complete frame's end offset.
func (r *Recording) Result() (size int64, limited bool, err error) {
	return r.frame, r.limited, r.err
}
