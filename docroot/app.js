
// copied from https://stackoverflow.com/a/10730417
function readCookie(name) {
    var nameEQ = name + "=";
    var ca = document.cookie.split(';');
    for (var i = 0; i < ca.length; i++) {
        var c = ca[i];
        while (c.charAt(0) == ' ') c = c.substring(1, c.length);
        if (c.indexOf(nameEQ) == 0) return c.substring(nameEQ.length, c.length);
    }
    return null;
}

function processIncoming(app, s) {
    let cmd, arg

    s = s.trim()
    if (s.length == 0) return
    let space = s.indexOf(' ')
    if (space == -1) {
        cmd = s
        arg = ''
    } else {
        cmd = s.substring(0, space)
        arg = s.substring(space+1).trim()
    }

    console.log('cmd=' + cmd + ',arg=' + arg)
    switch (cmd) {
        case 'screen':
            if (app.screen == 'display-player-results') {
                // set flag to disabled when we switch away from it
                app.displayplayerresultsdisabled = true
            }
            switch (arg) {
                case 'enter-identity':
                    app.enteridentitydisabled = false
                    app.setPinFromURL()
                    break
                case 'answer-question':
                    if (app.answerquestion.disabled) {
                        // we may have been disconnected - request for
                        // display-choices
                        app.sendCommand('query-display-choices')
                    }
                    break
                case 'display-player-results':
                    if (app.displayplayerresultsdisabled) {
                        // we may have been disconnected - request for results
                        app.sendCommand('query-player-results')
                    }
                    break
                case 'show-question-results':
                    // host may have been disconnected - request for question
                    // results
                    if (app.showquestionresultsdisabled) {
                        app.sendCommand('query-host-question-results')
                    }
                    break
                case 'show-game-results':
                    app.showgameresultsdisabled = false
                    break
            }
            app.screen = arg
            break

        case 'display-choices':
            app.answerquestion.answercount = parseInt(arg)
            app.answerquestion.disabled = false
            break

        case 'player-results':
            try {
                app.displayplayerresults = JSON.parse(arg)
                app.displayplayerresults.disabled = false
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'all-quizzes':
            try {
                app.selectquiz.quizzes = JSON.parse(arg)
                app.selectquizdisabled = false
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'lobby-game-metadata':
            try {
                app.gamelobby = JSON.parse(arg)
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'participants-list':
            try {
                app.gamelobby.players = JSON.parse(arg)

                if (app.gamelobby && app.gamelobby.players && app.gamelobby.players.length > 0) {
                    app.gamelobbydisabled = false
                }
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'show-question':
            try {
                app.showquestion = JSON.parse(arg)

                if (app.showquestion && app.showquestion.timeleft) {
                    app.timer = setInterval(function() {
                        if (app.showquestion && app.showquestion.timeleft > 0) {
                            app.showquestion.timeleft--

                            if (app.showquestion.timeleft == 0) {
                                app.stopCountdown()
                            }
                        }
                    }, 1000)
                }
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'players-answered':
            try {
                payload = JSON.parse(arg)
                if (payload != null && payload.answered != null && payload.totalplayers != null) {
                    app.showquestion.answered = payload.answered
                    app.showquestion.totalplayers = payload.totalplayers

                    if (payload.answered >= payload.totalplayers) {
                        app.stopCountdown()
                    }
                }
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'question-results':
            try {
                app.showquestionresults = JSON.parse(arg)
                app.showquestionresultsdisabled = false
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'show-winners':
            try {
                app.showgameresults = JSON.parse(arg)
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'error':
            app.showError(arg, app.screen)
            break

        default:
            console.log('oops!')
    }
}


var app = new Vue({
    el: '#app',

    data: {
        screen: 'start',
        selectquiz: {},
        gamelobby: { pin: 0, players: [] },
        gamelobbydisabled: true,
        enteridentity: { pin: 0, name: ''},
        enteridentitydisabled: true,
        answerquestion: { answercount: 0, disabled: true },
        selectquizdisabled: true,
        displayplayerresults: { correct: false, score: 0},
        displayplayerresultsdisabled: true,
        showquestion: { questionindex: 0, timeleft: 0, answered: 0, totalplayers:0, question: '', answers: [] },
        timer: null,
        timesUp: false,
        showquestionresults: { questionindex: 0, question: '', answers: [], correct: 0, votes: [], totalvotes: 0 },
        showquestionresultsdisabled: true,
        showgameresults: [],
        showgameresultsdisabled: true,
        error: { message: '', next: '', disabled: true },
        sessionid: '',
        conn: {}
    },

    mounted: function() {
        this.sessionid = readCookie('quizsession')
        if (this.sessionid == null || this.sessionid.length == 0) {
            this.showError('Please enable cookies')
            return
        }
        if (window["WebSocket"]) {
            var that = this
            that.conn = new WebSocket("ws://" + document.location.host + "/ws")
            that.conn.onopen = function (evt) {
                that.sendCommand("session " + that.sessionid)
            }
            that.conn.onclose = function (evt) {
                that.showError('Connection closed')
            }
            that.conn.onmessage = function (evt) {
                let messages = evt.data.split('\n')
                for (var i=0; i<messages.length; i++) {
                    processIncoming(that, messages[i])
                }
            }
        } else {
            this.showError('Your browser does not support WebSockets')
        }
    },

    methods: {

        showError: function(message, next) {
            this.error.disabled = false
            this.error.message = message
            this.error.next = next
            this.screen = 'error'
        },

        setPinFromURL: function() {
            let params=(new URL(document.location)).searchParams
            let pin=params.get("pin")
            if (pin != null) {
                this.enteridentity.pin = pin
            }
        },

        dismissError: function() {
            this.screen = this.error.next
            this.error.message = ''
            this.error.next = ''
            this.error.disabled = true
        },

        joinGame: function() {
            if (this.enteridentity.name.length == 0) {
                this.showError('Please fill in the name field', this.screen)
                return
            }
            this.sendCommand('join-game ' + JSON.stringify({name: this.enteridentity.name, pin: this.enteridentity.pin}))
        },

        sendAnswer: function(choice) {
            this.answerquestion.disabled = true
            this.sendCommand('answer ' + choice)
        },

        sendCommand: function(command) {
            this.conn.send(command)
        },

        hostGame: function() {
            this.sendCommand('host-game')
        },

        selectQuiz: function(quizid) {
            this.selectquizdisabled = true
            this.sendCommand('game-lobby ' + quizid)
        },

        startGame: function() {
            this.gamelobbydisabled = true
            this.sendCommand('start-game')
        },

        stopCountdown: function() {
            if (this.timer != null) {
                clearInterval(this.timer)
                this.timer = null
            }
            this.timesUp = true
            this.sendCommand('show-results')
        },

        hostNextQuestion: function() {
            this.showquestionresultsdisabled = true
            this.sendCommand('next-question')
        },

        deleteGame: function() {
            this.showgameresultsdisabled = true
            this.sendCommand('delete-game')
        },
    }
})
