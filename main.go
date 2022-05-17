package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"math/rand"
	"net/http"
	"time"
	_ "time/tzdata"

	"github.com/kwkoo/configparser"
	"github.com/kwkoo/go-quiz/internal"
	"github.com/kwkoo/go-quiz/internal/api"
	"github.com/kwkoo/go-quiz/internal/messaging"
	"github.com/kwkoo/go-quiz/internal/shutdown"
)

const authRealm = "Quiz Admin"

//go:embed docroot/*
var content embed.FS

func health(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "OK")
}

func main() {
	config := struct {
		Port           int    `default:"8080" usage:"HTTP listener port"`
		Docroot        string `usage:"HTML document root - will use the embedded docroot if not specified"`
		RedisHost      string `usage:"Redis host and port - will not connect to Redis if blank"`
		RedisPassword  string `usage:"Redis password"`
		AdminUser      string `default:"admin" usage:"Admin username"`
		AdminPassword  string `usage:"Admin password"`
		SessionTimeout int    `default:"900" usage:"Timeout in seconds both for in-memory sessions and sessions in the persistent store"`
		ReaperInterval int    `default:"60" usage:"Number of seconds between invocations of session reaper"`
	}{}
	if err := configparser.Parse(&config); err != nil {
		log.Fatal(err)
	}

	// initialize random number generator - used for shuffling answers
	rand.Seed(time.Now().UnixNano())

	shutdown.InitShutdownHandler()

	var filesystem http.FileSystem
	if len(config.Docroot) > 0 {
		log.Printf("using %s in the file system as the document root", config.Docroot)
		filesystem = http.Dir(config.Docroot)
	} else {
		log.Print("using the embedded filesystem as the docroot")

		subdir, err := fs.Sub(content, "docroot")
		if err != nil {
			log.Fatalf("could not get subdirectory: %v", err)
		}
		filesystem = http.FS(subdir)
	}

	auth := api.InitAuth(config.AdminUser, config.AdminPassword, authRealm)

	fileServer := http.FileServer(filesystem).ServeHTTP

	http.HandleFunc("/admin/", auth.BasicAuth(fileServer))

	http.HandleFunc("/healthz", health)

	cookieGen := api.InitCookieGenerator(fileServer)
	http.HandleFunc("/", cookieGen.ServeHTTP)

	mh := messaging.InitMessageHub()

	var persistenceEngine *internal.PersistenceEngine
	if len(config.RedisHost) > 0 {
		persistenceEngine = internal.InitRedis(config.RedisHost, config.RedisPassword)
	}
	quizzes, err := internal.InitQuizzes(mh, persistenceEngine)
	if err != nil {
		log.Fatal(err)
	}

	hub := internal.NewHub(mh, persistenceEngine)
	go func(shutdownChan chan struct{}) {
		hub.Run(shutdownChan)
	}(shutdown.GetShutdownChan())

	go func(shutdownChan chan struct{}) {
		quizzes.Run(shutdownChan)
	}(shutdown.GetShutdownChan())

	sessions := internal.InitSessions(mh, persistenceEngine, hub, auth, config.SessionTimeout, config.ReaperInterval)
	go func(shutdownChan chan struct{}) {
		sessions.Run(shutdownChan)
	}(shutdown.GetShutdownChan())

	games := internal.InitGames(mh, persistenceEngine)
	go func(shutdownChan chan struct{}) {
		games.Run(shutdownChan)
	}(shutdown.GetShutdownChan())

	api := api.InitRestApi(mh)
	http.HandleFunc("/api/", auth.BasicAuth(api.ServeHTTP))

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		internal.ServeWs(hub, w, r)
	})

	server := &http.Server{
		Addr: fmt.Sprintf(":%d", config.Port),
	}

	go func() {
		log.Printf("listening on port %v", config.Port)
		if err := server.ListenAndServe(); err != nil {
			if err == http.ErrServerClosed {
				log.Print("web server graceful shutdown")
				shutdown.NotifyShutdownComplete()
				return
			}
			log.Fatal(err)
		}
	}()

	go func() {
		<-shutdown.GetShutdownChan()
		log.Print("interrupt signal received, initiating web server shutdown...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	shutdown.WaitForShutdown()
	mh.Close()
	hub.ClosePersistenceEngine()
}
