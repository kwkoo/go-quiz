package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	_ "time/tzdata"

	"github.com/kwkoo/configparser"
	"github.com/kwkoo/go-quiz/pkg"
)

const authRealm = "Quiz Admin"

//go:embed docroot/*
var content embed.FS

func main() {
	config := struct {
		Port          int    `default:"8080" usage:"HTTP listener port"`
		Docroot       string `usage:"HTML document root - will use the embedded docroot if not specified"`
		RedisHost     string `default:"localhost:6379" usage:"Redis host and port"`
		RedisPassword string `usage:"Redis password"`
		AdminUser     string `default:"admin" usage:"Admin username"`
		AdminPassword string `usage:"Admin password"`
	}{}
	if err := configparser.Parse(&config); err != nil {
		log.Fatal(err)
	}

	pkg.InitShutdownHandler()

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

	auth := pkg.InitAuth(config.AdminUser, config.AdminPassword, authRealm)

	fileServer := http.FileServer(filesystem).ServeHTTP

	http.HandleFunc("/admin/", auth.BasicAuth(fileServer))

	cookieGen := pkg.InitCookieGenerator(fileServer)
	http.HandleFunc("/", cookieGen.ServeHTTP)

	hub := pkg.NewHub(config.RedisHost, config.RedisPassword, auth)
	go hub.Run()

	api := pkg.InitRestApi(hub)
	http.HandleFunc("/api/quizzes", auth.BasicAuth(api.Quizzes))
	http.HandleFunc("/api/sessions", auth.BasicAuth(api.Sessions))
	http.HandleFunc("/api/games", auth.BasicAuth(api.Games))

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		pkg.ServeWs(hub, w, r)
	})

	log.Printf("listening on port %v", config.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", config.Port), nil))
}
