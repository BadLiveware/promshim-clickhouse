package promshim

import (
	"bufio"
	"io"
)

func newScanner(body io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(body)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 8*1024*1024)
	return scanner
}
