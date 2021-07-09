package pkg

import "sync"

type Session struct {
	id      string
	client  *Client
	screen  string
	gamepin int
	name    string
}

type Sessions struct {
	mutex sync.RWMutex
	all   map[string]*Session
}

func InitSessions() *Sessions {
	return &Sessions{
		all: make(map[string]*Session),
	}
}

func (s *Sessions) NewSession(id string, client *Client, screen string) *Session {
	session := &Session{
		id:     id,
		client: client,
		screen: screen,
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.all[id] = session
	return session
}

func (s *Sessions) DeleteSession(id string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.all, id)
}

func (s *Sessions) GetScreenForSession(id string) string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	session, ok := s.all[id]
	if !ok {
		return ""
	}

	return session.screen
}

func (s *Sessions) UpdateScreenForSession(id, newscreen string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	session, ok := s.all[id]
	if !ok {
		return
	}

	session.screen = newscreen
}

func (s *Sessions) GetClientForSession(id string) *Client {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	session, ok := s.all[id]
	if !ok {
		return nil
	}

	return session.client
}

func (s *Sessions) UpdateClientForSession(id string, newclient *Client) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	session, ok := s.all[id]
	if !ok {
		return
	}

	session.client = newclient
}

func (s *Sessions) GetSession(id string) *Session {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	session, ok := s.all[id]

	if !ok {
		return nil
	}
	return session
}

func (s *Sessions) SetSessionName(id, name string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	session, ok := s.all[id]

	if !ok {
		return
	}
	session.name = name
}

func (s *Sessions) SetSessionScreen(id, screen string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	session, ok := s.all[id]

	if !ok {
		return
	}
	session.screen = screen
}

func (s *Sessions) SetSessionGamePin(id string, pin int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	session, ok := s.all[id]

	if !ok {
		return
	}
	session.gamepin = pin
}

func (s *Sessions) ConvertSessionIdsToNames(ids []string) []string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	names := []string{}
	for _, id := range ids {
		session, ok := s.all[id]
		if !ok {
			continue
		}
		names = append(names, session.name)
	}
	return names
}
