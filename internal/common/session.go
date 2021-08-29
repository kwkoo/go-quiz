package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type Session struct {
	Id      string      `json:"id"`
	Client  interface{} `json:"client"` // ugly hack to avoid circular imports
	Screen  string      `json:"screen"`
	Gamepin int         `json:"gamepin"`
	Name    string      `json:"name"`
	Admin   bool        `json:"admin"`
	Expiry  time.Time   `json:"expiry"`
}

func UnmarshalSession(b []byte) (*Session, error) {
	var session Session
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&session); err != nil {
		return nil, fmt.Errorf("error unmarshaling bytes to session: %v", err)
	}
	return &session, nil
}

func (s Session) Marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(&s); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (s *Session) Copy() Session {
	return Session{
		Id:      s.Id,
		Client:  s.Client,
		Screen:  s.Screen,
		Gamepin: s.Gamepin,
		Name:    s.Name,
		Admin:   s.Admin,
		Expiry:  s.Expiry,
	}
}
