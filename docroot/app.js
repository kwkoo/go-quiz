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


var app = new Vue({
    el: '#app',

    data: {
        screen: 'start',
        entrance: { data: {pin: 0, name: ''}, disabled: true },
        answerquestion: { answercount: 0, disabled: true },
        displayplayerresults: { data: {correct: false, score: 0}, disabled: true },
        authenticateuser: { username: '', password: '', previousscreen: '' },

        hostselectquiz: { quizzes: [], disabled: true },
        hostgamelobby: { data: { pin: 0, players: [] }, textarea: '', link: '', disabled: true },
        hostshowquestion: { data: { questionindex: 0, timeleft: 0, answered: 0, totalplayers:0, question: '', answers: [], votes: [], totalvotes: 0, totalquestions: 0, topscorers: [] }, timer: null },
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
            this.showError('Please enable cookies in your browser')
            return
        }
        if (window["WebSocket"]) {
            this.conn = new WebSocket("ws://" + document.location.host + "/ws")

            let that = this

            this.conn.onopen = function (evt) {
                that.showScreen('entrance')
                that.registerSession()
            }
            this.conn.onclose = function (evt) {
                that.showError('Connection closed')
            }
            this.conn.onmessage = function (evt) {
                let messages = evt.data.split('\n')
                for (var i=0; i<messages.length; i++) {
                    that.processIncoming(messages[i])
                }
            }
        } else {
            this.showError('Your browser does not support WebSockets')
        }
    },

    methods: {

        showScreen: function(target) {
            switch (target) {
                case 'hostselectquiz':
                    this.hostselectquiz.disabled = false
                    break
                case 'entrance':
                    this.entrance.disabled = false
                    break
            }
            this.screen = target
        },

        handleResize: function() {
            this.window.width = window.innerWidth;
            this.window.height = window.innerHeight;
        },

        registerSession: function() {
            this.sendCommand('session ' + this.sessionid)
        },

        showError: function(message, next) {
            this.error.disabled = false
            this.error.message = message
            if (next) {
                this.error.next = next
            }
            this.showScreen('error')
        },

        setPinFromURL: function() {
            let params=(new URL(document.location)).searchParams
            let pin=params.get("pin")
            if (pin != null) {
                this.entrance.data.pin = pin
            }
        },

        dismissError: function() {
            this.showScreen(this.error.next)
            this.error.message = ''
            this.error.next = ''
            this.error.disabled = true
        },

        cancelAuthentication: function() {
            this.showScreen(this.authenticateuser.previousscreen)
            this.authenticateuser.previousscreen = ''
        },

        adminLogin: function() {
            this.sendCommand('adminlogin ' + btoa(this.authenticateuser.username + ':' + this.authenticateuser.password))
        },

        joinGame: function() {
            if (this.entrance.data.name.length == 0) {
                this.showError('Please fill in the name field', this.screen)
                return
            }
            console.log('sending command to join game')
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

        processIncoming: function (s) {
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
                case 'reregistersession':
                    this.registerSession()
                    break
        
                case 'screen':
                    if (this.screen == 'displayplayerresults') {
                        // set flag to disabled when we switch away from it
                        this.displayplayerresults.disabled = true
                    }
                    switch (arg) {
                        case 'entrance':
                            this.entrance.disabled = false
                            this.setPinFromURL()
                            break
                        case 'answerquestion':
                            if (this.answerquestion.disabled) {
                                // we may have been disconnected - request for
                                // display-choices
                                this.sendCommand('query-display-choices')
                            }
                            break
                        case 'displayplayerresults':
                            if (this.displayplayerresults.disabled) {
                                // we may have been disconnected - request for results
                                this.sendCommand('query-player-results')
                            }
                            break
                        case 'hostshowresults':
                            // host may have been disconnected - request for question
                            // results
                            if (this.hostshowresults.disabled) {
                                this.sendCommand('query-host-results')
                            }
                            break
                        case 'hostshowgameresults':
                            this.hostshowgameresults.disabled = false
                            break
                        case 'authenticateuser':
                            this.authenticateuser.previousscreen = 'entrance'
                            this.authenticateuser.username = ''
                            this.authenticateuser.password = ''
                            break
                    }
                    this.showScreen(arg)
                    break
        
                case 'invalidcredentials':
                    this.showError('Invalid Credentials', this.screen)
                    break
        
                case 'display-choices':
                    this.answerquestion.answercount = parseInt(arg)
                    this.answerquestion.disabled = false
                    break
        
                case 'player-results':
                    try {
                        this.displayplayerresults.data = JSON.parse(arg)
                        this.displayplayerresults.disabled = false
                    } catch (err) {
                        console.log('err: ' + err)
                    }
                    break
        
                case 'all-quizzes':
                    try {
                        this.hostselectquiz.quizzes = JSON.parse(arg)
                        this.hostselectquiz.disabled = false
                    } catch (err) {
                        console.log('err: ' + err)
                    }
                    break
        
                case 'lobby-game-metadata':
                    try {
                        this.hostgamelobby.data = JSON.parse(arg)
                        let url = document.location.protocol + "//" + document.location.host + "?pin=" + this.hostgamelobby.data.pin
                        this.hostgamelobby.link = url
                        let qr = new QRious({
                            element: document.getElementById('qr'),
                            size: 300,
                            value: url
                        })
                        this.updateHostGameLobbyText()
                    } catch (err) {
                        console.log('err: ' + err)
                    }
                    break
        
                case 'participants-list':
                    try {
                        this.hostgamelobby.data.players = JSON.parse(arg)
                        this.updateHostGameLobbyText()
                    } catch (err) {
                        console.log('err: ' + err)
                    }
                    break
        
                case 'hostshowquestion':
                    try {
                        this.hostshowquestion.data = JSON.parse(arg)
        
                        if (this.hostshowquestion && this.hostshowquestion.data && this.hostshowquestion.data.timeleft) {
                            let that = this

                            this.hostshowquestion.timer = setInterval(function() {
                                if (that.hostshowquestion && that.hostshowquestion.data && that.hostshowquestion.data.timeleft > 0) {
                                    that.hostshowquestion.data.timeleft--
        
                                    if (that.hostshowquestion.data.timeleft == 0) {
                                        that.stopCountdown()
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
                            this.hostshowquestion.data.answered = payload.answered
                            this.hostshowquestion.data.totalplayers = payload.totalplayers
                            this.hostshowquestion.data.votes = payload.votes
                            this.hostshowquestion.data.totalvotes = payload.totalvotes
        
                            if (payload.allanswered) {
                                this.stopCountdown()
                            }
                        }
                    } catch (err) {
                        console.log('err: ' + err)
                    }
                    break
        
                case 'question-results':
                    try {
                        this.hostshowresults.data = JSON.parse(arg)
                        this.hostshowresults.disabled = false
                    } catch (err) {
                        console.log('err: ' + err)
                    }
                    break
        
                case 'show-winners':
                    try {
                        this.hostshowgameresults.data = JSON.parse(arg)
                    } catch (err) {
                        console.log('err: ' + err)
                    }
                    break
        
                case 'error':
                    try {
                        data = JSON.parse(arg)
                        this.showError(data.message, data.nextscreen)
                    } catch (err) {
                        console.log('err: ' + err)
                    }
                    break
        
                default:
                    console.log('oops!')
            }
        }
    }
})
