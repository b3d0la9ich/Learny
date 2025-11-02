package util

import "golang.org/x/crypto/bcrypt"

// Хэш пароля (bcrypt)
func HashPassword(p string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(b), err
}

// Проверка пароля
func CheckPassword(hash, p string) bool {
	// корректный вызов: CompareHashAndPassword
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}
