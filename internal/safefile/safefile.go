package safefile

import (
	"errors"
	"fmt"
	"io"
)

// Read lê path se for um arquivo comum de até max bytes. Arquivo inexistente
// satisfaz errors.Is(err, fs.ErrNotExist).
func Read(path string, max int64) ([]byte, error) {
	f, err := openNonBlocking(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("não é um arquivo comum")
	}
	if info.Size() > max {
		return nil, fmt.Errorf("arquivo maior que %d bytes", max)
	}

	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("arquivo maior que %d bytes", max)
	}
	return data, nil
}
