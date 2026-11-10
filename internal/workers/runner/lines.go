package runner

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// readLine liest eine Zeile von r. Eine Zeile über limit Bytes (ohne Zeilenende) wird bis zum
// Zeilenende verworfen und als oversize gemeldet; der Speicher bleibt dabei auf limit+1 begrenzt.
// Am Ende des Stroms liefert readLine io.EOF; eine unvollständige letzte Zeile wird noch geliefert.
func readLine(r *bufio.Reader, limit int) (line []byte, oversize bool, err error) {
	var buf []byte
	for {
		chunk, rerr := r.ReadSlice('\n')
		if !oversize {
			buf = append(buf, chunk...)
			if len(bytes.TrimRight(buf, "\r\n")) > limit {
				oversize, buf = true, nil
			}
		}
		switch {
		case rerr == nil:
			if oversize {
				return nil, true, nil
			}
			return bytes.TrimRight(buf, "\r\n"), false, nil
		case errors.Is(rerr, bufio.ErrBufferFull):
			continue
		case errors.Is(rerr, io.EOF):
			if oversize {
				return nil, true, nil
			}
			if len(buf) > 0 {
				return bytes.TrimRight(buf, "\r\n"), false, nil
			}
			return nil, false, io.EOF
		default:
			return nil, oversize, rerr
		}
	}
}
