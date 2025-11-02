package repo

import "database/sql"

type Repo struct {
	DB *sql.DB
}

func New(db *sql.DB) *Repo { return &Repo{DB: db} }
