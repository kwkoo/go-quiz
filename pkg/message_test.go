package pkg

import (
	"strings"
	"testing"
)

func TestMessage(t *testing.T) {
	name := "MyName"
	message := Message(name)
	if !strings.Contains(message, name) {
		t.Errorf("returned message %v did not contain name %v", message, name)
	}
}
