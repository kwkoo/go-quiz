package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/kwkoo/go-quiz/internal/common"
	"github.com/kwkoo/go-quiz/internal/messaging"
)

type Quizzes struct {
	all    map[int]common.Quiz
	mutex  sync.RWMutex
	engine *PersistenceEngine
	msghub *messaging.MessageHub
}

func InitQuizzes(msghub *messaging.MessageHub, engine *PersistenceEngine) (*Quizzes, error) {
	keys, err := engine.GetKeys("quiz")
	if err != nil {
		return nil, fmt.Errorf("could not retrieve keys from redis: %v", err)
	}

	all := make(map[int]common.Quiz)

	for _, key := range keys {
		data, err := engine.Get(key)
		if err != nil {
			log.Print(err.Error())
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		var quiz common.Quiz
		if err := dec.Decode(&quiz); err != nil {
			log.Printf("error parsing JSON from redis for key %s: %v", key, err)
			continue
		}
		all[quiz.Id] = quiz
	}

	log.Printf("ingested %d quizzes", len(all))
	return &Quizzes{
		all:    all,
		engine: engine,
		msghub: msghub,
	}, nil
}

func (q *Quizzes) Run(ctx context.Context, shutdownComplete func()) {
	topic := q.msghub.GetTopic(messaging.QuizzesTopic)
	for {
		select {
		case <-ctx.Done():
			log.Print("shutting down quiz handler")
			shutdownComplete()
			return
		case msg, ok := <-topic:
			if !ok {
				log.Printf("received empty message from %s", messaging.QuizzesTopic)
				continue
			}
			switch m := msg.(type) {
			case common.SendQuizzesToClientMessage:
				q.processSendQuizzesToClientMessage(m)
			case common.LookupQuizForGameMessage:
				q.processLookupQuizForGameMessage(m)
			case common.DeleteQuizMessage:
				q.processDeleteQuizMessage(m)
			case *common.GetQuizzesMessage:
				q.processGetQuizzesMessage(m)
			case *common.GetQuizMessage:
				q.processGetQuizMessage(m)
			case *common.AddQuizMessage:
				q.processAddQuizMessage(m)
			case *common.UpdateQuizMessage:
				q.processUpdateQuizMessage(m)
			default:
				log.Printf("unrecognized message type %T received on %s topic", msg, messaging.QuizzesTopic)
			}
		}
	}
}

func (q *Quizzes) processUpdateQuizMessage(msg *common.UpdateQuizMessage) {
	msg.Result <- q.update(msg.Quiz)
	close(msg.Result)
}

func (q *Quizzes) processAddQuizMessage(msg *common.AddQuizMessage) {
	msg.Result <- q.add(msg.Quiz)
	close(msg.Result)
}

func (q *Quizzes) processGetQuizMessage(msg *common.GetQuizMessage) {
	quiz, err := q.get(msg.Quizid)
	msg.Result <- common.GetQuizResult{
		Quiz:  quiz,
		Error: err,
	}
	close(msg.Result)
}

func (q *Quizzes) processGetQuizzesMessage(msg *common.GetQuizzesMessage) {
	msg.Result <- q.getQuizzes()
	close(msg.Result)
}

func (q *Quizzes) processDeleteQuizMessage(msg common.DeleteQuizMessage) {
	q.delete(msg.Quizid)
}

func (q *Quizzes) processLookupQuizForGameMessage(msg common.LookupQuizForGameMessage) {
	quiz, err := q.get(msg.Quizid)
	if err != nil {
		q.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error getting quiz in new game: " + err.Error(),
			Nextscreen: "host-select-quiz",
		})
		return
	}

	q.msghub.Send(messaging.GamesTopic, common.SetQuizForGameMessage{
		Pin:  msg.Pin,
		Quiz: quiz,
	})

	q.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
		Sessionid:  msg.Sessionid,
		Nextscreen: "host-game-lobby",
	})
}

func (q *Quizzes) processSendQuizzesToClientMessage(msg common.SendQuizzesToClientMessage) {
	type quizMeta struct {
		Id   int    `json:"id"`
		Name string `json:"name"`
	}
	ml := []quizMeta{}
	for _, quiz := range q.getQuizzes() {
		ml = append(ml, quizMeta{
			Id:   quiz.Id,
			Name: quiz.Name,
		})
	}

	encoded, err := common.ConvertToJSON(&ml)
	if err != nil {
		q.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    fmt.Sprintf("error encoding json: %v", err),
			Nextscreen: "host-select-quiz",
		})
		return
	}
	q.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
		Clientid: msg.Clientid,
		Message:  "all-quizzes " + encoded,
	})
}

// called by REST API
func (q *Quizzes) getQuizzes() []common.Quiz {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	ids := make([]int, len(q.all))

	i := 0
	for k := range q.all {
		ids[i] = k
		i++
	}
	sort.Ints(ids)

	r := make([]common.Quiz, len(ids))
	for i, id := range ids {
		r[i] = q.all[id]
	}
	return r
}

// called by REST API
func (q *Quizzes) get(id int) (common.Quiz, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	quiz, ok := q.all[id]
	if !ok {
		return common.Quiz{}, fmt.Errorf("could not find quiz with id %d", id)
	}
	return quiz, nil
}

func (q *Quizzes) delete(id int) {
	q.mutex.Lock()
	delete(q.all, id)
	q.mutex.Unlock()

	if q.engine != nil {
		q.engine.Delete(fmt.Sprintf("quiz:%d", id))
	}
}

// called by REST API
func (q *Quizzes) add(quiz common.Quiz) error {
	var err error
	quiz.Id, err = q.nextID()
	if err != nil {
		return err
	}

	if q.engine != nil {
		encoded, err := quiz.Marshal()
		if err != nil {
			return fmt.Errorf("error converting quiz to JSON: %v", err)
		}
		if err := q.engine.Set(fmt.Sprintf("quiz:%d", quiz.Id), encoded, 0); err != nil {
			return fmt.Errorf("error persisting quiz to redis: %v", err)
		}
	}

	q.mutex.Lock()
	q.all[quiz.Id] = quiz
	q.mutex.Unlock()
	return nil
}

// called by REST API
func (q *Quizzes) update(quiz common.Quiz) error {
	q.mutex.Lock()
	q.all[quiz.Id] = quiz
	q.mutex.Unlock()

	if q.engine != nil {
		encoded, err := quiz.Marshal()
		if err != nil {
			return fmt.Errorf("error converting quiz to JSON: %v", err)
		}
		if err := q.engine.Set(fmt.Sprintf("quiz:%d", quiz.Id), encoded, 0); err != nil {
			return fmt.Errorf("error persisting quiz to redis: %v", err)
		}
	}
	return nil
}

func (q *Quizzes) nextID() (int, error) {
	if q.engine == nil {
		q.mutex.RLock()
		defer q.mutex.RUnlock()
		highest := 0
		for key := range q.all {
			if key > highest {
				highest = key
			}
		}
		return highest + 1, nil
	}
	id, err := q.engine.Incr("quizid")
	if err != nil {
		return 0, fmt.Errorf("error generating quiz ID from persistent store: %v", err)
	}
	return id, nil
}
