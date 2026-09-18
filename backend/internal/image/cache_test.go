package image

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestValidateRejectsCorruptAndTruncatedImages(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	valid := encoded.Bytes()
	if err := Validate(valid); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)/2] ^= 255
	for _, data := range [][]byte{nil, []byte("<html>challenge</html>"), valid[:len(valid)-15], corrupt} {
		if Validate(data) == nil {
			t.Fatal("accepted corrupt image")
		}
	}
}
