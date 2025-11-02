package httpx

import (
	"context"
	"net/http"

	a "learny/internal/auth"
	"learny/internal/repo"
)

type ctxKey int

const (
	ctxUserID ctxKey = iota + 1
)

// WithUser — задел на будущее; сейчас просто прокидывает дальше.
func WithUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := a.CurrentUserID(r); ok {
			ctx := context.WithValue(r.Context(), ctxUserID, id)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.CurrentUserID(r); !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireRole(repo *repo.Repo, roles ...string) func(http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid, ok := a.CurrentUserID(r)
			if !ok {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			role, err := repo.GetUserRole(r.Context(), uid)
			if err != nil {
				http.Error(w, "role error", http.StatusForbidden)
				return
			}
			if _, ok := allowed[role]; !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
