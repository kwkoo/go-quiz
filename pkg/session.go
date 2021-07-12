package pkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

const sessionExpiry = 600 // session expiry in seconds

type Session struct {
	Id      string
	Client  *Client
	Screen  string
	Gamepin int
	Name    string
}

func unmarshalSession(b []byte) (*Session, error) {
	var session Session
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&session); err != nil {
		return nil, fmt.Errorf("error unmarshaling bytes to session: %v", err)
	}
	return &session, nil
}

func (s Session) marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(&s); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

type Sessions struct {
	mutex  sync.RWMutex
	all    map[string]*Session
	engine *PersistenceEngine
}

func InitSessions(engine *PersistenceEngine) *Sessions {
	sessions := Sessions{
		all:    make(map[string]*Session),
		engine: engine,
	}

	keys, err := engine.GetKeys("session")
	if err != nil {
		log.Printf("error retrieving session keys from persistent store: %v", err)
		return &sessions
	}

	log.Printf("persistent store contains %d sessions - clearing clients from all sessions...", len(keys))
	for _, key := range keys {
		key = key[len("session:"):]
		sessions.UpdateClientForSession(key, nil)
	}

	return &sessions
}

func (s *Sessions) NewSession(id string, client *Client, screen string) *Session {
	session := &Session{
		Id:     id,
		Client: client,
		Screen: screen,
	}

	s.mutex.Lock()
	s.all[id] = session
	s.mutex.Unlock()

	s.persist(session)
	return session
}

func (s *Sessions) persist(session *Session) {
	if s.engine == nil {
		return
	}

	data, err := session.marshal()
	if err != nil {
		log.Printf("error encoding session %s to JSON: %v", session.Id, err)
		return
	}

	if err := s.engine.Set(fmt.Sprintf("session:%s", session.Id), data, sessionExpiry); err != nil {
		log.Printf("error persisting session %s to redis: %v", session.Id, err)
	}
}

func (s *Sessions) DeleteSession(id string) {
	s.mutex.Lock()
	delete(s.all, id)
	s.mutex.Unlock()

	s.engine.Delete(fmt.Sprintf("session:%s", id))
}

func (s *Sessions) GetScreenForSession(id string) string {
	session := s.GetSession(id)

	if session == nil {
		return ""
	}

	return session.Screen
}

func (s *Sessions) GetGamePinForSession(id string) int {
	session := s.GetSession(id)

	if session == nil {
		return -1
	}

	return session.Gamepin
}

func (s *Sessions) UpdateScreenForSession(id, newscreen string) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Screen = newscreen
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) GetClientForSession(id string) *Client {
	session := s.GetSession(id)

	if session == nil {
		return nil
	}

	return session.Client
}

func (s *Sessions) UpdateClientForSession(id string, newclient *Client) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Client = newclient
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) GetSession(id string) *Session {
	s.mutex.RLock()
	session, ok := s.all[id]
	s.mutex.RUnlock()

	if ok {
		return session
	}

	if s.engine == nil {
		return nil
	}

	// session doesn't exist in memory - check if it's available in the
	// storage engine
	key := fmt.Sprintf("session:%s", id)
	data, err := s.engine.Get(key)
	if err != nil {
		return nil
	}

	decoded, err := unmarshalSession(data)
	if err != nil {
		log.Printf("error decoding session from redis: %v", err)
		return nil
	}

	s.mutex.Lock()
	s.all[id] = decoded
	s.mutex.Unlock()
	return decoded
}

func (s *Sessions) RegisterSessionInGame(id, name string, pin int) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Name = name
	session.Gamepin = pin
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) DeregisterGameFromSession(id string) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Gamepin = 0
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) SetSessionScreen(id, screen string) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Screen = screen
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) SetSessionGamePin(id string, pin int) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Gamepin = pin
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) ConvertSessionIdsToNames(ids []string) []string {
	names := []string{}

	for _, id := range ids {
		session := s.GetSession(id)
		if session == nil {
			continue
		}
		names = append(names, session.Name)
	}

	return names
}
