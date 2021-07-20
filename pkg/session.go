package pkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

const reaperInterval = 60

type Session struct {
	Id      string    `json:"id"`
	Client  *Client   `json:"client"`
	Screen  string    `json:"screen"`
	Gamepin int       `json:"gamepin"`
	Name    string    `json:"name"`
	Admin   bool      `json:"admin"`
	Expiry  time.Time `json:"expiry"`
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

func (s *Session) copy() Session {
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

type Sessions struct {
	mutex          sync.RWMutex
	all            map[string]*Session
	engine         *PersistenceEngine
	auth           *Auth
	sessionTimeout int
}

func InitSessions(engine *PersistenceEngine, auth *Auth, sessionTimeout int, shutdownArtifacts *ShutdownArtifacts) *Sessions {
	log.Printf("session timeout set to %d seconds", sessionTimeout)

	sessions := Sessions{
		all:            make(map[string]*Session),
		engine:         engine,
		auth:           auth,
		sessionTimeout: sessionTimeout,
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

	// todo: kick off session reaper here
	shutdownArtifacts.Wg.Add(1)
	go func() {
		for {
			select {
			case <-shutdownArtifacts.Ch:
				log.Printf("shutting down session reaper")
				shutdownArtifacts.Wg.Done()
				return
			case <-time.After(reaperInterval * time.Second):
				sessions.expireSessions()
			}
		}
	}()

	return &sessions
}

func (s *Sessions) NewSession(id string, client *Client, screen string) *Session {
	session := &Session{
		Id:     id,
		Client: client,
		Screen: screen,
		Expiry: time.Now().Add(time.Duration(s.sessionTimeout) * time.Second),
	}

	s.mutex.Lock()
	s.all[id] = session
	s.mutex.Unlock()

	s.persist(session)
	return session
}

func (s *Sessions) ExtendSessionExpiry(id string) {
	session := s.GetSession(id)

	if session == nil {
		return
	}

	s.persist(session)
}

func (s *Sessions) expireSessions() {
	now := time.Now()
	s.mutex.Lock()
	for id, session := range s.all {
		if now.After(session.Expiry) {
			delete(s.all, id)
			log.Printf("expiring session %s", id)
		}
	}
	s.mutex.Unlock()
}

func (s *Sessions) persist(session *Session) {
	s.mutex.Lock()
	session.Expiry = time.Now().Add(time.Duration(s.sessionTimeout) * time.Second)
	s.mutex.Unlock()

	if s.engine == nil {
		return
	}

	data, err := session.marshal()
	if err != nil {
		log.Printf("error encoding session %s to JSON: %v", session.Id, err)
		return
	}

	if err := s.engine.Set(fmt.Sprintf("session:%s", session.Id), data, s.sessionTimeout); err != nil {
		log.Printf("error persisting session %s to redis: %v", session.Id, err)
	}
}

func (s *Sessions) getAll() []Session {
	all := []Session{}
	s.mutex.RLock()
	for _, v := range s.all {
		all = append(all, v.copy())
	}
	s.mutex.RUnlock()
	return all
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

func (s *Sessions) GetNameForSession(id string) string {
	session := s.GetSession(id)

	if session == nil {
		return ""
	}

	return session.Name
}

func (s *Sessions) GetClientForSession(id string) *Client {
	session := s.GetSession(id)

	if session == nil {
		return nil
	}

	return session.Client
}

func (s *Sessions) UpdateClientForSession(id string, newclient *Client) {
	if id == "" {
		return
	}
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
	session.Gamepin = -1
	session.Screen = "entrance"
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

func (s *Sessions) SessionIsAdmin(id string) bool {
	session := s.GetSession(id)
	if session == nil {
		return false
	}
	return session.Admin
}

// Credentials is in the basic auth format (base64 encoding of
// username:password).
// Returns true if user is authenticated.
func (s *Sessions) AuthenticateAdmin(id, credentials string) bool {
	session := s.GetSession(id)
	if session.Admin {
		return true
	}
	if s.auth.Base64Authenticated(credentials) {
		s.mutex.Lock()
		session.Admin = true
		s.mutex.Unlock()
		s.persist(session)
		return true
	}
	return false
}
