package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"

	httpx "learny/internal/http"
	"learny/internal/repo"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@db:5432/edu?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}

	// ---- авто-сид вопросов из questions_all.json ----
	if err := autoSeedQuestions(db); err != nil {
		log.Printf("autoSeedQuestions error: %v", err)
	}

	rp := repo.New(db)

	// БЕЗ FuncMap, просто парсим шаблоны
	tpl := template.Must(
		template.New("").ParseGlob("web/templates/*.tmpl.html"),
	)

	srv := &httpx.Server{DB: db, Repo: rp, T: tpl}

	mux := http.NewServeMux()
	srv.Routes(mux)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", httpx.WithUser(mux)))
}

// autoSeedQuestions читает questions_all.json и заливает вопросы в БД,
// если таблица questions пока пустая.
func autoSeedQuestions(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var cnt int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM questions`).Scan(&cnt); err != nil {
		return err
	}
	if cnt > 0 {
		log.Printf("auto-seed: questions already exist (%d), skip", cnt)
		return nil
	}

	raw, err := os.ReadFile("questions_all.json")
	if err != nil {
		return err
	}

	type item struct {
		CourseID   int64           `json:"course_id"`
		Topic      string          `json:"topic"`
		QType      string          `json:"qtype"`
		Difficulty int             `json:"difficulty"`
		Payload    json.RawMessage `json:"payload_json"`
	}

	var items []item
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO questions (course_id, topic, qtype, difficulty, payload_json)
		VALUES ($1, $2, $3, $4, $5)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, it := range items {
		if _, err := stmt.ExecContext(ctx,
			it.CourseID,
			it.Topic,
			it.QType,
			it.Difficulty,
			it.Payload,
		); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	log.Printf("auto-seed: inserted %d questions from questions_all.json", len(items))
	return nil
}
