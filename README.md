# Go Quiz App

A Kahoot clone with a UI based on [Ethan Brimhall's kahoot-clone-nodejs](https://github.com/ethanbrimhall/kahoot-clone-nodejs).

## Resources

* [gorilla websocket example](https://github.com/gorilla/websocket/tree/master/examples/chat)
* [redigo docs](https://pkg.go.dev/github.com/gomodule/redigo/redis)
* [redigo example code](https://github.com/pete911/examples-redigo)


## Quiz Host Messages

* *host starts in connecting to server screen*
* host → server: session SESSION-ID
* server → host: screen entrance
* *host clicks on host a game*
* host → server: host-game
* server → host: all-quizzes [{"id":1,"name":"Quiz 1"},{"id":2,"name":"Quiz 2"}]
* server → host: screen hostselectquiz
* host → server: hostgamelobby 1
* server → host: lobby-game-metadata {"id":1,"name":"Quiz 1","pin":1234}
* server → host: screen hostgamelobby
* server → host: participants-list ["user1", "user2", "user3"]
* host → server: start-game
* server → host: hostshowquestion {"questionindex":0, "timeleft":30, "answered":0, "totalplayers":5, "question":"What did I eat for breakfast?", "answers":["answer 0", "answer 1", "answer 2", "answer 3"], "votes":[0,0,0,0]}
* server → host: screen hostshowquestion
* server → host: players-answered {"answered": 2, "totalplayers": 10, "votes":[0,0,0,0]}
* server → host: players-answered {"answered": 3, "totalplayers": 10, "votes":[0,0,0,0]}
* *time runs out, stop timer, enable show results button*
* host → server: show-results
* server → host: question-results {"questionindex":0, "question":"What did I eat for breakfast?", "answers":["answer 0", "answer 1", "answer 2", "answer 3"], "correct": 0, "votes":[1,2,3,3], "totalvotes": 9}
* server → host: screen hostshowresults
* host → server: next-question
* server → host: hostshowquestion {"questionindex":1, "timeleft":30, "answered":0, "totalplayers":5, "question":"What did I eat for lunch?", "answers":["answer 0", "answer 1", "answer 2", "answer 3"]}
* server → host: screen hostshowquestion
* server → host: players-answered {"answered": 10, "total": 10}
* server → host: stop-timer
* *enable show results button*
* host → server: show-results
* host → server: question-results {"questionindex":1, "question":"What did I eat for lunch?", "answers":["answer 0", "answer 1", "answer 2", "answer 3"], "correct": 0, "votes":[1,2,3,4], "totalvotes": 10}
* server → host: screen hostshowresults
* host → server: next-question
* server → host: show-winners [{"name": "user1", "score": 500}, {"name": "user2", "score": 300}, {"name": "user3", "score": 200}]
* server → host: screen hostshowgameresults
* host → server: delete-game
* server → host: all-quizzes [{"id":1,"name":"Quiz 1"},{"id":2,"name":"Quiz 2"}]
* server → host: screen hostselectquiz

Other messages:

* host → server: query-host-results - sent when the host reconnects while his state is in the hostshowresults screen


## Player Messages

* *player starts in connecting to server screen*
* player → server: session SESSION-ID
* server → player: screen entrance
* player → server: join-game {"pin": 1234, "name": "user1"}
* server → player: screen waitforgamestart
* server → player: display-choices 4
* server → player: screen answerquestion
* player → server: answer 2
* server → player: screen waitforquestionend
* server → player: player-results {"correct": true, "score": 180}
* server → player: screen displayplayerresults
* server → player: screen answerquestion
* *player does not answer the question*
* server → player: player-results {"correct": false, "score": 180}
* *the game ends*
* server → player: screen entrance

Other messages:

* player → server: query-display-choices - sent when the player reconnects while his state is in the answerquestion screen
* player → server: query-player-results - sent when the player reconnects while his state is in the displayplayerresults screen


## Host Authentication Messages

* host → server: host-game
* *server checks host's session and finds that the Admin flag is false*
* server → host: screen authenticateuser
* *host enters invalid credentials*
* host → server: adminlogin BASE64ENCODEDCREDENTIALS
* server → host: invalidcredentials
* *host enters valid credentials*
* host → server: adminlogin BASE64ENCODEDCREDENTIALS
* server → host: screen hostselectquiz
