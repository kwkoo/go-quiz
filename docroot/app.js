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
            if (app.screen == 'displayplayerresults') {
                // set flag to disabled when we switch away from it
                app.displayplayerresults.disabled = true
            }
            switch (arg) {
                case 'entrance':
                    app.entrance.disabled = false
                    app.setPinFromURL()
                    break
                case 'answerquestion':
                    if (app.answerquestion.disabled) {
                        // we may have been disconnected - request for
                        // display-choices
                        app.sendCommand('query-display-choices')
                    }
                    break
                case 'displayplayerresults':
                    if (app.displayplayerresults.disabled) {
                        // we may have been disconnected - request for results
                        app.sendCommand('query-player-results')
                    }
                    break
                case 'hostshowresults':
                    // host may have been disconnected - request for question
                    // results
                    if (app.hostshowresults.disabled) {
                        app.sendCommand('query-host-results')
                    }
                    break
                case 'hostshowgameresults':
                    app.hostshowgameresults.disabled = false
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
                app.displayplayerresults.data = JSON.parse(arg)
                app.displayplayerresults.disabled = false
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'all-quizzes':
            try {
                app.hostselectquiz.quizzes = JSON.parse(arg)
                app.hostselectquiz.disabled = false
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'lobby-game-metadata':
            try {
                app.hostgamelobby.data = JSON.parse(arg)
                let url = document.location.protocol + "//" + document.location.host + "?pin=" + app.hostgamelobby.data.pin
                app.hostgamelobby.link = url
                let qr = new QRious({
                    element: document.getElementById('qr'),
                    size: 300,
                    value: url
                })
                app.updateHostGameLobbyText()
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'participants-list':
            try {
                app.hostgamelobby.data.players = JSON.parse(arg)
                app.updateHostGameLobbyText()
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'hostshowquestion':
            try {
                app.hostshowquestion.data = JSON.parse(arg)

                if (app.hostshowquestion && app.hostshowquestion.data && app.hostshowquestion.data.timeleft) {
                    app.hostshowquestion.timer = setInterval(function() {
                        if (app.hostshowquestion && app.hostshowquestion.data && app.hostshowquestion.data.timeleft > 0) {
                            app.hostshowquestion.data.timeleft--

                            if (app.hostshowquestion.data.timeleft == 0) {
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
                if (payload != null && payload.answered != null && payload.totalplayers != null && payload.votes != null) {
                    app.hostshowquestion.data.answered = payload.answered
                    app.hostshowquestion.data.totalplayers = payload.totalplayers
                    app.hostshowquestion.data.votes = payload.votes
                    app.hostshowquestion.data.totalvotes = payload.totalvotes

                    if (payload.allanswered) {
                        app.stopCountdown()
                    }
                }
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'question-results':
            try {
                app.hostshowresults.data = JSON.parse(arg)
                app.hostshowresults.disabled = false
            } catch (err) {
                console.log('err: ' + err)
            }
            break

        case 'show-winners':
            try {
                app.hostshowgameresults.data = JSON.parse(arg)
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
        entrance: { data: {pin: 0, name: ''}, disabled: true },
        answerquestion: { answercount: 0, disabled: true },
        displayplayerresults: { data: {correct: false, score: 0}, disabled: true },

        hostselectquiz: { quizzes: [], disabled: true },
        hostgamelobby: { data: { pin: 0, players: [] }, textarea: '', link: '', disabled: true },
        hostshowquestion: { data: { questionindex: 0, timeleft: 0, answered: 0, totalplayers:0, question: '', answers: [], votes: [], totalvotes: 0, totalquestions: 0 }, timer: null },
        hostshowresults: { data: { questionindex: 0, question: '', answers: [], correct: 0, votes: [], totalvotes: 0, totalquestions: 0 }, disabled: true },
        hostshowgameresults: { data: [], disabled: true },
        error: { message: '', next: '', disabled: true },
        sessionid: '',
        conn: {},
        window: { width: 0, height: 0 }
    },

    created: function() {
        window.addEventListener('resize', this.handleResize);
        this.handleResize();
    },

    destroyed: function() {
        window.removeEventListener('resize', this.handleResize);
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

        handleResize: function() {
            this.window.width = window.innerWidth;
            this.window.height = window.innerHeight;
        },

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
                this.entrance.data.pin = pin
            }
        },

        dismissError: function() {
            this.screen = this.error.next
            this.error.message = ''
            this.error.next = ''
            this.error.disabled = true
        },

        joinGame: function() {
            if (this.entrance.data.name.length == 0) {
                this.showError('Please fill in the name field', this.screen)
                return
            }
            this.sendCommand('join-game ' + JSON.stringify({name: this.entrance.data.name, pin: parseInt(this.entrance.data.pin)}))
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

        cancelGame: function() {
            this.sendCommand('cancel-game')
        },

        sendHostBackToStart: function() {
            this.sendCommand('host-back-to-start')
        },

        hostSelectQuiz: function(quizid) {
            this.hostselectquiz.disabled = true
            this.sendCommand('hostgamelobby ' + quizid)
        },

        updateHostGameLobbyText: function() {
            if (this.hostgamelobby && this.hostgamelobby.data && this.hostgamelobby.data.players) {
                let playerstext = ''
                this.hostgamelobby.data.players.forEach((player, index) => {
                    if (index > 0) playerstext += '\n'
                    playerstext += player
                })
                this.hostgamelobby.textarea = playerstext

                if (this.hostgamelobby.data.players.length > 0) {
                    this.hostgamelobby.disabled = false
                }
            }
        },

        startGame: function() {
            this.hostgamelobby.disabled = true
            this.sendCommand('start-game')
        },

        stopCountdown: function() {
            if (this.hostshowquestion.timer != null) {
                clearInterval(this.hostshowquestion.timer)
                this.hostshowquestion.timer = null
            }
            this.sendCommand('show-results')
        },

        hostNextQuestion: function() {
            this.hostshowresults.disabled = true
            this.sendCommand('next-question')
        },

        deleteGame: function() {
            this.hostshowgameresults.disabled = true
            this.sendCommand('delete-game')
        },
    }
})
