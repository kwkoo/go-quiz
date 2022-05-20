package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kwkoo/go-quiz/internal/api"
	"github.com/kwkoo/go-quiz/internal/common"
	"github.com/kwkoo/go-quiz/internal/messaging"
)

type webSocketRegistry interface {
	DeregisterClientID([]uint64)
}

type Sessions struct {
	msghub         *messaging.MessageHub
	wsRegistry     webSocketRegistry
	mutex          sync.RWMutex
	all            map[string]*common.Session
	clientids      map[uint64]*common.Session
	engine         *PersistenceEngine
	auth           *api.Auth
	sessionTimeout int
	reaperInterval int
}

func InitSessions(msghub *messaging.MessageHub, engine *PersistenceEngine, wsRegistry webSocketRegistry, auth *api.Auth, sessionTimeout int, reaperInterval int) *Sessions {
	log.Printf("session timeout set to %d seconds", sessionTimeout)

	sessions := Sessions{
		msghub:         msghub,
		wsRegistry:     wsRegistry,
		all:            make(map[string]*common.Session),
		clientids:      make(map[uint64]*common.Session),
		engine:         engine,
		auth:           auth,
		sessionTimeout: sessionTimeout,
		reaperInterval: reaperInterval,
	}

	keys, err := engine.GetKeys("session")
	if err != nil {
		log.Printf("error retrieving session keys from persistent store: %v", err)
		return &sessions
	}

	log.Printf("persistent store contains %d sessions - clearing clients from all sessions...", len(keys))
	for _, key := range keys {
		key = key[len("session:"):]
		sessions.updateClientIDForSession(key, 0)
	}

	return &sessions
}

func (s *Sessions) RunSessionReaper(ctx context.Context, shutdownComplete func()) {
	log.Printf("session reaper will run every %d seconds", s.reaperInterval)
	timeout := time.After(time.Duration(s.reaperInterval) * time.Second)
	for {
		select {
		case <-ctx.Done():
			log.Print("shutting down session reaper")
			shutdownComplete()
			return
		case <-timeout:
			log.Print("running session reaper")
			s.expireSessions()
			timeout = time.After(time.Duration(s.reaperInterval) * time.Second)
		}
	}
}

func (s *Sessions) Run(ctx context.Context, shutdownComplete func()) {
	fromClients := s.msghub.GetTopic(messaging.IncomingMessageTopic)
	sessionsHub := s.msghub.GetTopic(messaging.SessionsTopic)

	for {
		select {
		case msg, ok := <-fromClients:
			if !ok {
				log.Printf("received empty message from %s", messaging.IncomingMessageTopic)
				continue
			}
			switch m := msg.(type) {
			case *ClientCommand:
				s.processClientCommand(m)
			default:
				log.Printf("unrecognized message type %T received on %s topic", msg, messaging.IncomingMessageTopic)
			}
		case msg, ok := <-sessionsHub:
			if !ok {
				log.Printf("received empty message from %s", messaging.SessionsTopic)
				continue
			}
			switch m := msg.(type) {
			case common.ErrorToSessionMessage:
				s.processErrorToSessionMessage(m)
			case common.BindGameToSessionMessage:
				s.processBindGameToSessionMessage(m)
			case common.SessionToScreenMessage:
				s.processSessionToScreenMessage(m)
			case common.SetSessionScreenMessage:
				s.processSetSessionScreenMessage(m)
			case common.SessionMessage:
				s.processSessionMessage(m)
			case common.SetSessionGamePinMessage:
				s.processSetSessionGamePinMessage(m)
			case common.DeregisterGameFromSessionsMessage:
				s.processDeregisterGameFromSessionsMessage(m)
			case common.ExtendSessionExpiryMessage:
				s.processExtendSessionExpiryMessage(m)
			case common.DeleteSessionMessage:
				s.processDeleteSessionMessage(m)
			case common.DeregisterClientMessage:
				s.processDeregisterClientMessage(m)
			case *common.GetSessionsMessage:
				s.processGetSessionsMessage(m)
			default:
				log.Printf("unrecognized message type %T received on %s topic", msg, messaging.SessionsTopic)
			}
		case <-ctx.Done():
			log.Print("shutting down sessions handler")
			shutdownComplete()
			return
		}
	}
}

func (s *Sessions) processGetSessionsMessage(msg *common.GetSessionsMessage) {
	msg.Result <- s.getAll()
	close(msg.Result)
}

func (s *Sessions) processDeregisterClientMessage(msg common.DeregisterClientMessage) {
	log.Printf("session deregister client %d", msg.Clientid)
	s.mutex.RLock()
	session, ok := s.clientids[msg.Clientid]
	s.mutex.RUnlock()
	if ok {
		s.updateClientIDForSession(session.Id, 0)
	}

	s.mutex.Lock()
	delete(s.clientids, msg.Clientid)
	s.mutex.Unlock()
}

func (s *Sessions) processDeleteSessionMessage(msg common.DeleteSessionMessage) {
	session := s.getSession(msg.Sessionid)
	if session == nil {
		return
	}
	s.mutex.Lock()
	delete(s.clientids, session.ClientId)
	s.mutex.Unlock()
	s.deleteSession(msg.Sessionid)
}

func (s *Sessions) processDeregisterGameFromSessionsMessage(msg common.DeregisterGameFromSessionsMessage) {
	for _, sessionid := range msg.Sessions {
		s.deregisterGameFromSession(sessionid)
	}
}

func (s *Sessions) processSetSessionGamePinMessage(msg common.SetSessionGamePinMessage) {
	s.setSessionGamePin(msg.Sessionid, msg.Pin)
}

func (s *Sessions) processSessionMessage(msg common.SessionMessage) {
	sess := s.getSession(msg.Sessionid)
	if sess == nil {
		// session doesn't exist
		log.Printf("session %s does not exist", msg.Sessionid)
		return
	}
	s.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
		Clientid: sess.ClientId,
		Message:  msg.Message,
	})
}

func (s *Sessions) processSetSessionScreenMessage(msg common.SetSessionScreenMessage) {
	s.setSessionScreen(msg.Sessionid, msg.Nextscreen)
}

func (s *Sessions) processSessionToScreenMessage(msg common.SessionToScreenMessage) {
	session := s.getSession(msg.Sessionid)
	if session == nil {
		// session doesn't exist
		log.Printf("session %s does not exist", msg.Sessionid)
		return
	}

	// session is valid from this point on

	// ensure that session is admin if trying to access host screens
	if strings.HasPrefix(msg.Nextscreen, "host") && !session.Admin {
		msg.Nextscreen = "authenticate-user"
	}

	switch msg.Nextscreen {

	case "host-select-quiz":
		s.msghub.Send(messaging.QuizzesTopic, common.SendQuizzesToClientMessage{
			Clientid:  session.ClientId,
			Sessionid: session.Id,
		})

	case "host-game-lobby":
		s.msghub.Send(messaging.GamesTopic, common.SendGameMetadataMessage{
			Clientid:  session.ClientId,
			Sessionid: session.Id,
			Pin:       session.Gamepin,
		})

	case "host-show-question":
		s.msghub.Send(messaging.GamesTopic, common.HostShowQuestionMessage{
			Clientid:  session.ClientId,
			Sessionid: session.Id,
			Pin:       session.Gamepin,
		})

	case "host-show-game-results":
		s.msghub.Send(messaging.GamesTopic, common.HostShowGameResultsMessage{
			Clientid:  session.ClientId,
			Sessionid: session.Id,
			Pin:       session.Gamepin,
		})

		// end of switch
	}

	s.setSessionScreen(session.Id, msg.Nextscreen)

	s.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
		Clientid: session.ClientId,
		Message:  "screen " + msg.Nextscreen,
	})
}

func (s *Sessions) processBindGameToSessionMessage(msg common.BindGameToSessionMessage) {
	s.registerSessionInGame(msg.Sessionid, msg.Name, msg.Pin)
}

func (s *Sessions) processErrorToSessionMessage(msg common.ErrorToSessionMessage) {
	if msg.Nextscreen != "" {
		s.setSessionScreen(msg.Sessionid, msg.Nextscreen)
	}

	clientid := s.getClientIDForSession(msg.Sessionid)
	if clientid == 0 {
		// session is not bound to a client
		log.Printf("session %s does not have a client", msg.Sessionid)
		return
	}

	s.msghub.Send(messaging.ClientHubTopic, common.ClientErrorMessage{
		Clientid:   clientid,
		Sessionid:  msg.Sessionid,
		Message:    msg.Message,
		Nextscreen: msg.Nextscreen,
	})
}

func (s *Sessions) processExtendSessionExpiryMessage(msg common.ExtendSessionExpiryMessage) {
	s.extendSessionExpiry(msg.Sessionid)
}

func (s *Sessions) processClientCommand(m *ClientCommand) {
	s.mutex.RLock()
	session, ok := s.clientids[m.client]
	s.mutex.RUnlock()
	if !ok {
		// client hasn't identified themselves yet
		if m.cmd == "session" {
			if len(m.arg) == 0 || len(m.arg) > 64 {
				s.msghub.Send(messaging.ClientHubTopic, common.ClientErrorMessage{
					Clientid:   m.client,
					Sessionid:  "",
					Message:    "invalid session ID",
					Nextscreen: "entrance",
				})
				return
			}

			clientid := m.client
			sessionid := m.arg

			session := s.getSession(sessionid)
			if session == nil {
				session = s.newSession(sessionid, m.client, "entrance")
			} else {
				if session.ClientId != 0 {
					s.msghub.Send(messaging.ClientHubTopic, common.ClientErrorMessage{
						Clientid:   m.client,
						Sessionid:  "",
						Message:    "you have another active session - disconnect that session before reconnecting",
						Nextscreen: "",
					})

					return
				}
				s.updateClientIDForSession(session.Id, clientid)
			}
			s.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
				Sessionid:  sessionid,
				Nextscreen: session.Screen,
			})
			return
		}
		s.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
			Clientid: m.client,
			Message:  "register-session",
		})
		return
	}

	sessionid := ""
	if session != nil {
		sessionid = session.Id
	}

	clientid := m.client

	if session == nil {
		s.msghub.Send(messaging.ClientHubTopic, common.ClientErrorMessage{
			Clientid:   m.client,
			Sessionid:  "",
			Message:    "session does not exist",
			Nextscreen: "",
		})

		return
	}

	// session is valid from this point on

	switch m.cmd {

	case "admin-login":
		if s.authenticateAdmin(sessionid, m.arg) {
			s.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
				Sessionid:  sessionid,
				Nextscreen: "host-select-quiz",
			})

			return
		}

		// invalid credentials
		s.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
			Clientid: clientid,
			Message:  "invalid-credentials",
		})
		return

	case "join-game":
		pinfo := struct {
			Pin  int    `json:"pin"`
			Name string `json:"name"`
		}{}
		dec := json.NewDecoder(strings.NewReader(m.arg))
		if err := dec.Decode(&pinfo); err != nil {
			s.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  sessionid,
				Message:    "could not decode json: " + err.Error(),
				Nextscreen: "entrance",
			})
			return
		}
		if len(pinfo.Name) == 0 {
			s.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  sessionid,
				Message:    "name is missing",
				Nextscreen: "entrance",
			})
			return
		}

		s.msghub.Send(messaging.GamesTopic, common.AddPlayerToGameMessage{
			Sessionid: sessionid,
			Name:      pinfo.Name,
			Pin:       pinfo.Pin,
		})

		return

	case "query-display-choices":
		// player may have been disconnected - now they need to know how many
		// answers to enable
		if session.Gamepin < 0 {
			s.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  sessionid,
				Message:    "could not get game pin for this session",
				Nextscreen: "entrance",
			})
			return
		}
		s.msghub.Send(messaging.GamesTopic, common.QueryDisplayChoicesMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Pin:       session.Gamepin,
		})
		return

	case "query-player-results":
		// player may have been disconnected - now they need to know about
		// their results
		if session.Gamepin < 0 {
			s.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  sessionid,
				Message:    "could not get game pin for this session",
				Nextscreen: "entrance",
			})
			return
		}

		s.msghub.Send(messaging.GamesTopic, common.QueryPlayerResultsMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Pin:       session.Gamepin,
		})
		return

	case "answer":
		playerAnswer, err := strconv.Atoi(m.arg)
		if err != nil {
			s.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  sessionid,
				Message:    "could not parse answer",
				Nextscreen: "",
			})
			return
		}

		if session.Gamepin < 0 {
			s.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  sessionid,
				Message:    "could not get game pin for this session",
				Nextscreen: "entrance",
			})
			return
		}

		s.msghub.Send(messaging.GamesTopic, common.RegisterAnswerMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Pin:       session.Gamepin,
			Answer:    playerAnswer,
		})
		return

	case "host-back-to-start":
		s.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
			Sessionid:  sessionid,
			Nextscreen: "entrance",
		})
		return

	case "cancel-game":
		s.msghub.Send(messaging.GamesTopic, common.CancelGameMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Pin:       session.Gamepin,
		})
		return

	case "host-game":
		s.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
			Sessionid:  sessionid,
			Nextscreen: "host-select-quiz",
		})
		return

	case "host-game-lobby":
		quizid, err := strconv.Atoi(m.arg)
		if err != nil {
			s.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  sessionid,
				Message:    "expected int argument",
				Nextscreen: "host-select-quiz",
			})
			return
		}

		s.msghub.Send(messaging.GamesTopic, common.HostGameLobbyMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Quizid:    quizid,
		})
		return

	case "start-game":
		s.msghub.Send(messaging.GamesTopic, common.StartGameMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Pin:       session.Gamepin,
		})
		return

	case "show-results":
		s.msghub.Send(messaging.GamesTopic, common.ShowResultsMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Pin:       session.Gamepin,
		})
		return

	case "query-host-results":
		s.msghub.Send(messaging.GamesTopic, common.QueryHostResultsMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Pin:       session.Gamepin,
		})
		return

	case "next-question":
		s.msghub.Send(messaging.GamesTopic, common.NextQuestionMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Pin:       session.Gamepin,
		})
		return

	case "delete-game":
		s.msghub.Send(messaging.GamesTopic, common.DeleteGameMessage{
			Clientid:  clientid,
			Sessionid: sessionid,
			Pin:       session.Gamepin,
		})
		return

	default:
		s.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  sessionid,
			Message:    "invalid command",
			Nextscreen: "",
		})
		return
	}
}

func (s *Sessions) newSession(id string, clientid uint64, screen string) *common.Session {
	session := &common.Session{
		Id:       id,
		ClientId: clientid,
		Screen:   screen,
		Expiry:   time.Now().Add(time.Duration(s.sessionTimeout) * time.Second),
	}

	s.mutex.Lock()
	s.all[id] = session
	s.clientids[clientid] = session
	s.mutex.Unlock()

	s.persist(session)

	return session
}

func (s *Sessions) extendSessionExpiry(id string) {
	session := s.getSession(id)

	if session == nil {
		return
	}

	s.persist(session)
}

func (s *Sessions) expireSessions() {
	clientids := []uint64{}
	now := time.Now()
	s.mutex.RLock()
	for id, session := range s.all {
		if now.After(session.Expiry) {
			s.msghub.Send(messaging.SessionsTopic, common.DeleteSessionMessage{
				Sessionid: id,
			})
			clientids = append(clientids, session.ClientId)
			log.Printf("expiring session %s", id)
		}
	}
	s.mutex.RUnlock()

	if len(clientids) > 0 {
		log.Printf("expiring %d session(s)", len(clientids))
		s.wsRegistry.DeregisterClientID(clientids)
	}
}

func (s *Sessions) persist(session *common.Session) {
	s.mutex.Lock()
	session.Expiry = time.Now().Add(time.Duration(s.sessionTimeout) * time.Second)
	s.mutex.Unlock()

	if s.engine == nil {
		return
	}

	data, err := session.Marshal()
	if err != nil {
		log.Printf("error encoding session %s to JSON: %v", session.Id, err)
		return
	}

	if err := s.engine.Set(fmt.Sprintf("session:%s", session.Id), data, s.sessionTimeout); err != nil {
		log.Printf("error persisting session %s to redis: %v", session.Id, err)
	}
}

// called by REST API
func (s *Sessions) getAll() []common.Session {
	all := []common.Session{}
	s.mutex.RLock()
	for _, v := range s.all {
		all = append(all, v.Copy())
	}
	s.mutex.RUnlock()
	return all
}

func (s *Sessions) deleteSession(id string) {
	s.mutex.Lock()
	delete(s.all, id)
	s.mutex.Unlock()

	s.engine.Delete(fmt.Sprintf("session:%s", id))
}

func (s *Sessions) getClientIDForSession(id string) uint64 {
	session := s.getSession(id)

	if session == nil {
		return 0
	}

	return session.ClientId
}

func (s *Sessions) updateClientIDForSession(id string, newclientid uint64) {
	if id == "" {
		return
	}
	session := s.getSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	oldclientid := session.ClientId
	if oldclientid != 0 {
		delete(s.clientids, oldclientid)
	}
	session.ClientId = newclientid
	if newclientid != 0 {
		s.clientids[newclientid] = session
	}
	s.mutex.Unlock()
	s.persist(session)
}

// also called by REST API
func (s *Sessions) getSession(id string) *common.Session {
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

	decoded, err := common.UnmarshalSession(data)
	if err != nil {
		log.Printf("error decoding session from redis: %v", err)
		return nil
	}

	s.mutex.Lock()
	s.all[id] = decoded
	if decoded.ClientId != 0 {
		s.clientids[decoded.ClientId] = decoded
	}
	s.mutex.Unlock()
	return decoded
}

func (s *Sessions) registerSessionInGame(id, name string, pin int) {
	session := s.getSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Name = name
	session.Gamepin = pin
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) deregisterGameFromSession(id string) {
	session := s.getSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Gamepin = -1
	session.Screen = "entrance"
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) setSessionScreen(id, screen string) {
	session := s.getSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Screen = screen
	s.mutex.Unlock()
	s.persist(session)
}

func (s *Sessions) setSessionGamePin(id string, pin int) {
	session := s.getSession(id)

	if session == nil {
		return
	}

	s.mutex.Lock()
	session.Gamepin = pin
	s.mutex.Unlock()
	s.persist(session)
}

// Credentials is in the basic auth format (base64 encoding of
// username:password).
// Returns true if user is authenticated.
func (s *Sessions) authenticateAdmin(id, credentials string) bool {
	session := s.getSession(id)
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
