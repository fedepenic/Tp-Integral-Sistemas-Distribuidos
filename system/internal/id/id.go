package id

import (
	"crypto/rand"
	"fmt"
)

func New() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func Child(parent string, partition int) string {
	return fmt.Sprintf("%s:%d",
		parent,
		partition,
	)
}
