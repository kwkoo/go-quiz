package messaging

import (
	"log"
	"sync"
)

const chanSize = 20

// topics
const (
	IncomingMessageTopic = "from-clients"
	ClientHubTopic       = "client-hub"
	SessionsTopic        = "sessions-hub"
	GamesTopic           = "games-hub"
	QuizzesTopic         = "quizzes"
)

type MessageHub struct {
	mux   sync.Mutex
	chans map[string](chan interface{})
}

func InitMessageHub() *MessageHub {
	return &MessageHub{
		chans: make(map[string]chan interface{}),
	}
}

func (mh *MessageHub) Send(topicname string, msg interface{}) {
	topic := mh.GetTopic(topicname)
	topic <- msg
}

func (mh *MessageHub) Close() {
	for _, c := range mh.chans {
		close(c)
	}
	log.Print("MessageHub shutdown complete")
}

func (mh *MessageHub) GetTopic(name string) chan interface{} {
	mh.mux.Lock()
	defer mh.mux.Unlock()
	topic, ok := mh.chans[name]
	if ok {
		return topic
	}
	topic = make(chan interface{}, chanSize)
	mh.chans[name] = topic
	log.Printf("created topic %s", name)
	return topic
}
