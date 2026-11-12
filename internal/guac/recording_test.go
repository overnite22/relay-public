package guac

import (
	"bytes"
	"strings"
	"testing"
)

func TestRecordingFiltersStreamsAndKeepsCompleteFrames(t *testing.T) {
	var output bytes.Buffer
	sync := New("sync", "100")
	img := New("img", "2", "14", "0", "image/png", "0", "0")
	blob := New("blob", "2", "pixels")
	end := New("end", "2")
	expected := img.Encode() + blob.Encode() + end.Encode() + sync.Encode()
	rec := NewRecording(&output, int64(len(expected)+15))
	for _, in := range []Instruction{
		New("clipboard", "2", "text/plain"), New("blob", "2", "secret"), New("end", "2"),
		img, blob, end, sync,
		New("file", "2", "application/octet-stream", "secret.txt"), New("blob", "2", "secret"),
		New("end", "2"), New("key", "65", "1"),
		New("rect", "0", "0", "0", "1000", "1000"),
	} {
		rec.Capture([]byte(in.Encode()), append([]string{in.Opcode}, in.Args...))
	}
	size, limited, err := rec.Result()
	if err != nil || !limited || size != int64(len(expected)) || !strings.HasPrefix(output.String(), expected) || strings.Contains(output.String(), "secret") {
		t.Fatalf("result = %d, %v, %v, %q", size, limited, err, output.String())
	}
}
