

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
        conn: null,
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
        this.showScreen('start')
    },

    methods: {

        // copied from https://stackoverflow.com/a/10730417
        readCookie: function(name) {
            var nameEQ = name + "="
            var ca = document.cookie.split(';')
            for (var i = 0; i < ca.length; i++) {
                var c = ca[i]
                while (c.charAt(0) == ' ') c = c.substring(1, c.length)
                if (c.indexOf(nameEQ) == 0) return c.substring(nameEQ.length, c.length)
            }
            return null
        },

        showScreen: function(target) {
            this.screen = target

            switch (target) {
                case 'start':
                    this.setupConn()
                    break
                case 'entrance':
                    this.entrance.disabled = false
                    this.$nextTick(() => this.$refs.playername.focus())
                    break
                case 'authenticate-user':
                    this.$nextTick(() => this.$refs.adminname.focus())
                    break
                case 'host-select-quiz':
                    this.hostselectquiz.disabled = false
                    break
            }
        },

        setupConn: function() {
            this.sessionid = this.readCookie('quizsession')
            if (this.sessionid == null || this.sessionid.length == 0) {
                this.showError('Please enable cookies in your browser')
                return
            }
            if (this.conn != null) {
                try {
                    this.conn.close()
                } catch (err) {
                    console.log('Exception closing websocket: ' + err)
                }
                this.conn = null
            }
            if (window["WebSocket"]) {
                this.conn = new WebSocket((document.location.protocol.startsWith('https')?'wss':'ws') + '://' + document.location.host + "/ws")
    
                let that = this
    
                this.conn.onopen = function (evt) {
                    that.registerSession()
                }
                this.conn.onclose = function (evt) {
                    that.conn = null
                    that.showError('Connection closed - click OK to reconnect', 'start')
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
            if (next && next != '') {
                this.error.next = next
                this.$nextTick(() => this.$refs.errorok.focus())
            } else {
                this.error.next = null
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
            this.sendCommand('admin-login ' + btoa(this.authenticateuser.username + ':' + this.authenticateuser.password))
        },

        joinGame: function() {
            if (this.entrance.data.name.length == 0) {
                this.showError('Please fill in the name field', 'entrance')
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
            this.sendCommand('host-game-lobby ' + quizid)
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

        saveWinners: function() {
            this.exportObject(this.hostshowgameresults.data, 'winners.json')
        },

        exportObject: function(obj, filename) {
            // copied from https://stackoverflow.com/a/30832210
            let file = new Blob([JSON.stringify(obj)], {type: 'application/json'})
            if (window.navigator.msSaveOrOpenBlob) // IE10+
                window.navigator.msSaveOrOpenBlob(file, filename)
            else { // others
                let a = document.createElement('a')
                let url = URL.createObjectURL(file)
                a.href = url;
                a.download = filename
                document.body.appendChild(a)
                a.click()
                setTimeout(function() {
                    document.body.removeChild(a)
                    window.URL.revokeObjectURL(url)
                }, 0)
            }
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
                case 'register-session':
                    this.registerSession()
                    break
        
                case 'screen':
                    if (this.screen == 'display-player-results') {
                        // set flag to disabled when we switch away from it
                        this.displayplayerresults.disabled = true
                    }
                    switch (arg) {
                        case 'entrance':
                            this.entrance.disabled = false
                            this.setPinFromURL()
                            break
                        case 'answer-question':
                            if (this.answerquestion.disabled) {
                                // we may have been disconnected - request for
                                // display-choices
                                this.sendCommand('query-display-choices')
                            }
                            break
                        case 'display-player-results':
                            if (this.displayplayerresults.disabled) {
                                // we may have been disconnected - request for results
                                this.sendCommand('query-player-results')
                            }
                            break
                        case 'host-show-results':
                            // host may have been disconnected - request for question
                            // results
                            if (this.hostshowresults.disabled) {
                                this.sendCommand('query-host-results')
                            }
                            break
                        case 'host-show-game-results':
                            this.hostshowgameresults.disabled = false
                            break
                        case 'authenticate-user':
                            this.authenticateuser.previousscreen = 'entrance'
                            this.authenticateuser.username = ''
                            this.authenticateuser.password = ''
                            break
                    }
                    this.showScreen(arg)
                    break
        
                case 'invalid-credentials':
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

                        // size QR code based on viewport
                        let qrSize = Math.min(window.innerWidth, window.innerHeight) * 0.6

                        let qr = new QRious({
                            element: document.getElementById('qr'),
                            size: qrSize,
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
        
                case 'host-show-question':
                    try {
                        this.hostshowquestion.data = JSON.parse(arg)
        
                        if (this.hostshowquestion && this.hostshowquestion.data && this.hostshowquestion.data.timeleft) {
                            let that = this

                            if (this.hostshowquestion.timer != null) {
                                clearInterval(this.hostshowquestion.timer)
                                this.hostshowquestion.timer = null
                            }

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
