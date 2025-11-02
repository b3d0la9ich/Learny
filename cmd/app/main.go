package main

import (
	"database/sql"
	"html/template"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"

	httpx "learny/internal/http"
	"learny/internal/repo"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		// если без Docker, поменяй db->localhost
		dsn = "postgres://postgres:postgres@db:5432/edu?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil { log.Fatal(err) }
	if err := db.Ping(); err != nil { log.Fatal(err) }

	rp := repo.New(db)

	// парсим ВСЕ *.tmpl.html (без кастомных функций — они не нужны)
	tpl := template.Must(template.New("").ParseGlob("web/templates/*.tmpl.html"))

	srv := &httpx.Server{DB: db, Repo: rp, T: tpl}

	mux := http.NewServeMux()
	srv.Routes(mux)

	// статика
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", httpx.WithUser(mux)))
}
