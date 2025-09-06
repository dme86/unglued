package util

import (
	"bytes"
	"compress/gzip"
	"io"
)

func GzipEncode(s string) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return buf.Bytes()
}
func GzipDecode(b []byte) (string, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil { return "", err }
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil { return "", err }
	return string(out), nil
}

