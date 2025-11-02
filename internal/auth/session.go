package auth

import (
	"net/http"
	"strconv"
)

const cookieName = "sid"

// Демоверсия: просто кладём userID в cookie (в проде использовать подпись/шифрование).
func SetSession(w http.ResponseWriter, userID int64) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    strconv.FormatInt(userID, 10),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
	})
}

func CurrentUserID(r *http.Request) (int64, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(c.Value, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}
